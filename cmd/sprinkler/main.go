// Command sprinkler is the irrigation controller: one binary with the
// scheduler, valve driver, HTTP API and web UI.
//
//	sprinkler serve  -config sprinkler.toml
//	sprinkler import -from ./os-copy -db sprinkler.db [-force]
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"

	"sprinkler/internal/api"
	"sprinkler/internal/app"
	"sprinkler/internal/hw"
	"sprinkler/internal/importos"
	"sprinkler/internal/runner"
	"sprinkler/internal/store"
	"sprinkler/internal/tlscert"
)

var version = "dev"

// Config is the small hardware/port file; everything else lives in the DB.
type Config struct {
	Listen  string `toml:"listen"`
	DB      string `toml:"db"`
	Outputs struct {
		Driver   string `toml:"driver"`
		Chip     string `toml:"chip"`
		Count    int    `toml:"count"`
		ShiftReg struct {
			Latch, Data, Clock, OE int
		} `toml:"shiftreg"`
		Relay struct {
			Pins      []int `toml:"pins"`
			ActiveLow bool  `toml:"active_low"`
		} `toml:"gpiorelay"`
	} `toml:"outputs"`
	Inputs struct {
		Rain struct {
			Pin      int    `toml:"pin"`
			Normally string `toml:"normally"`
			Sim      bool   `toml:"sim"`
		} `toml:"rain"`
	} `toml:"inputs"`
	TLS struct {
		Listen string   `toml:"listen"` // e.g. ":443"; empty = no HTTPS
		Hosts  []string `toml:"hosts"`  // names/IPs for the certificate; default: hostname(.local) + current IPs
		Dir    string   `toml:"dir"`    // where the local CA and certificate live; default: <db dir>/tls
		Cert   string   `toml:"cert"`   // optional: your own certificate instead of the local CA
		Key    string   `toml:"key"`
	} `toml:"tls"`
}

func defaultConfig() Config {
	var c Config
	c.Listen = ":8080"
	c.DB = "sprinkler.db"
	c.Outputs.Driver = "sim"
	c.Outputs.Chip = "gpiochip0"
	c.Outputs.Count = 8
	c.Outputs.ShiftReg.Latch, c.Outputs.ShiftReg.Data, c.Outputs.ShiftReg.Clock, c.Outputs.ShiftReg.OE = 22, 27, 4, 17
	c.Inputs.Rain.Pin = -1
	c.Inputs.Rain.Normally = "open"
	return c
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:], log)
	case "import":
		err = importCmd(os.Args[2:], log)
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  sprinkler serve  [-config sprinkler.toml] [-listen :8080] [-db sprinkler.db]
  sprinkler import -from DIR [-db sprinkler.db] [-tz Europe/Sofia] [-force] [-dry-run]
  sprinkler version
`)
}

func importCmd(args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	from := fs.String("from", "", "directory with a COPY of nvm.dat, stns.dat and logs/")
	dbPath := fs.String("db", "sprinkler.db", "database file")
	tz := fs.String("tz", "Europe/Sofia", "time zone OpenSprinkler's clock followed")
	force := fs.Bool("force", false, "replace existing zones, programs and history")
	dry := fs.Bool("dry-run", false, "only print what would be imported")
	fs.Parse(args)
	if *from == "" {
		return errors.New("-from is required")
	}
	d, err := importos.Parse(*from, importos.Options{Timezone: *tz})
	if err != nil {
		return err
	}
	loc, _ := time.LoadLocation(*tz)
	fmt.Print(d.Report(loc))
	if *dry {
		return nil
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := importos.Import(context.Background(), st, d, *force); err != nil {
		return err
	}
	fmt.Printf("\nImported into %s\n", *dbPath)
	return nil
}

func openOutputs(c Config, log *slog.Logger) (hw.Outputs, error) {
	switch c.Outputs.Driver {
	case "sim":
		return hw.NewSim(c.Outputs.Count, log), nil
	case "shiftreg":
		sr := c.Outputs.ShiftReg
		return hw.OpenShiftReg(hw.ShiftRegConfig{Chip: c.Outputs.Chip, Count: c.Outputs.Count, Latch: sr.Latch, Data: sr.Data, Clock: sr.Clock, OE: sr.OE})
	case "gpiorelay":
		return hw.OpenRelay(hw.RelayConfig{Chip: c.Outputs.Chip, Pins: c.Outputs.Relay.Pins, ActiveLow: c.Outputs.Relay.ActiveLow})
	}
	return nil, fmt.Errorf("unknown output driver %q", c.Outputs.Driver)
}

func serve(args []string, log *slog.Logger) (err error) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "", "TOML config (hardware, port)")
	listen := fs.String("listen", "", "override listen address")
	dbPath := fs.String("db", "", "override database file")
	fs.Parse(args)

	cfg := defaultConfig()
	if *cfgPath != "" {
		if _, err := toml.DecodeFile(*cfgPath, &cfg); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *dbPath != "" {
		cfg.DB = *dbPath
	}

	// Outputs first: they must be all-off before anything else happens.
	out, err := openOutputs(cfg, log)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = out.AllOff()
			panic(p)
		}
	}()
	defer func() {
		if cerr := out.Close(); cerr != nil {
			log.Error("closing outputs", "err", cerr)
		}
	}()
	if err := out.AllOff(); err != nil {
		return fmt.Errorf("all-off at start: %w", err)
	}
	log.Info("outputs ready", "driver", out.Name(), "count", out.Count())

	st, err := store.Open(cfg.DB)
	if err != nil {
		return err
	}
	defer st.Close()
	settings, err := st.Settings()
	if err != nil {
		return err
	}
	loc, err := settings.Location()
	if err != nil {
		return err
	}
	r, err := runner.New(out, st, loc, log)
	if err != nil {
		return err
	}
	a, err := app.New(st, r, log)
	if err != nil {
		return err
	}
	a.Version = version
	switch {
	case cfg.Inputs.Rain.Sim || (cfg.Outputs.Driver == "sim" && cfg.Inputs.Rain.Pin < 0):
		a.SimSensor = &hw.SimInput{}
	case cfg.Inputs.Rain.Pin >= 0:
		in, err := hw.OpenInput(cfg.Outputs.Chip, cfg.Inputs.Rain.Pin, cfg.Inputs.Rain.Normally)
		if err != nil {
			return err
		}
		defer in.Close()
		a.Sensor = in
	}
	if err := a.Recover(time.Now()); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	r.Alive = watchdog(log)
	runnerDone := make(chan error, 1)
	go func() { runnerDone <- r.Run(ctx, time.Now) }()
	go a.Loop(ctx)

	handler := api.New(a, out.Count(), log)
	var servers []*http.Server
	fail := func(err error) error {
		stop()
		<-runnerDone
		return err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fail(err)
	}
	servers = append(servers, srv)
	log.Info("listening", "addr", ln.Addr().String(), "db", cfg.DB, "version", version)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http", "err", err)
			stop()
		}
	}()
	if cfg.TLS.Listen != "" {
		tcfg, caPEM, hosts, err := tlsConfig(cfg)
		if err != nil {
			return fail(fmt.Errorf("tls: %w", err))
		}
		handler.SetCA(caPEM)
		tsrv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, TLSConfig: tcfg}
		tln, err := net.Listen("tcp", cfg.TLS.Listen)
		if err != nil {
			return fail(err)
		}
		servers = append(servers, tsrv)
		log.Info("listening https", "addr", tln.Addr().String(), "hosts", hosts)
		go func() {
			if err := tsrv.ServeTLS(tln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("https", "err", err)
				stop()
			}
		}()
	}
	sdNotify("READY=1")
	<-ctx.Done()
	log.Info("shutting down: all valves off")
	sdNotify("STOPPING=1")
	shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shCtx)
	}
	return <-runnerDone
}

// tlsConfig loads the configured certificate, or the local CA's.
func tlsConfig(cfg Config) (*tls.Config, []byte, []string, error) {
	if cfg.TLS.Cert != "" {
		c, err := tls.LoadX509KeyPair(cfg.TLS.Cert, cfg.TLS.Key)
		if err != nil {
			return nil, nil, nil, err
		}
		return &tls.Config{Certificates: []tls.Certificate{c}, MinVersion: tls.VersionTLS12}, nil, nil, nil
	}
	dir := cfg.TLS.Dir
	if dir == "" {
		dir = filepath.Join(filepath.Dir(cfg.DB), "tls")
	}
	hosts := cfg.TLS.Hosts
	if len(hosts) == 0 {
		hosts = defaultHosts()
	}
	b, err := tlscert.Ensure(dir, hosts, time.Now())
	if err != nil {
		return nil, nil, nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{b.Cert}, MinVersion: tls.VersionTLS12}, b.CAPEM, b.Hosts, nil
}

// defaultHosts: the hostname, hostname.local (mDNS), localhost and every
// non-link-local address the machine has now.
func defaultHosts() []string {
	hosts := []string{"localhost", "127.0.0.1"}
	if h, err := os.Hostname(); err == nil && h != "" {
		hosts = append(hosts, h, h+".local")
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
				hosts = append(hosts, ipn.IP.String())
			}
		}
	}
	return hosts
}

// sdNotify sends a message to systemd if NOTIFY_SOCKET is set.
func sdNotify(msg string) {
	sock := os.Getenv("NOTIFY_SOCKET")
	if sock == "" {
		return
	}
	if sock[0] == '@' {
		sock = "\x00" + sock[1:]
	}
	c, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		return
	}
	defer c.Close()
	c.Write([]byte(msg))
}

// watchdog returns a callback for the runner loop that pings systemd's
// watchdog at most every few seconds, only while the loop is alive.
func watchdog(log *slog.Logger) func() {
	if os.Getenv("WATCHDOG_USEC") == "" {
		return nil
	}
	var last time.Time
	return func() {
		if time.Since(last) >= 5*time.Second {
			last = time.Now()
			sdNotify("WATCHDOG=1")
		}
	}
}
