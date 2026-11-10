package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestDeployConfigs(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	pi, err := loadConfig("../../deploy/sprinklerpi.toml", log)
	if err != nil {
		t.Fatal(err)
	}
	sr := pi.Outputs.ShiftReg
	if pi.Outputs.Driver != "shiftreg" || sr.Latch != 22 || sr.Data != 27 || sr.Clock != 4 || sr.OE != 17 || pi.Outputs.Count != 8 {
		t.Fatalf("Pi outputs: %+v", pi.Outputs)
	}
	if pi.Inputs.Rain.Pin != 14 || pi.Inputs.Rain.Normally != "open" || pi.Inputs.Rain.Sim {
		t.Fatalf("Pi rain sensor: %+v", pi.Inputs.Rain)
	}
	if pi.Listen != ":80" || pi.TLS.Listen != "" || pi.DB != "/var/lib/sprinkler/sprinkler.db" {
		t.Fatalf("Pi listen/db: %q %q %q", pi.Listen, pi.TLS.Listen, pi.DB)
	}
	if !pi.Clock.WaitSync {
		t.Fatal("clock wait_sync must default to on")
	}

	dev, err := loadConfig("../../deploy/dev.toml", log)
	if err != nil {
		t.Fatal(err)
	}
	if dev.Outputs.Driver != "sim" || !dev.Inputs.Rain.Sim || dev.Outputs.Chip != "gpiochip0" {
		t.Fatalf("dev config: %+v", dev)
	}
	if logs.Len() != 0 {
		t.Fatalf("deploy configs have unknown keys:\n%s", logs.String())
	}
}

func TestLoadConfigUnknownKeyAndErrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.toml")
	if err := os.WriteFile(p, []byte("listen = \":9\"\n[clock]\nwait_snyc = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	cfg, err := loadConfig(p, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9" || !cfg.Clock.WaitSync {
		t.Fatalf("cfg %+v", cfg)
	}
	if !strings.Contains(logs.String(), "clock.wait_snyc") {
		t.Fatalf("typo not reported: %q", logs.String())
	}
	if _, err := loadConfig(filepath.Join(dir, "missing.toml"), quiet()); err == nil {
		t.Fatal("missing file must fail")
	}
	if err := os.WriteFile(p, []byte("listen = \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(p, quiet()); err == nil {
		t.Fatal("bad TOML must fail")
	}
	if cfg, err := loadConfig("", quiet()); err != nil || cfg.Outputs.Driver != "sim" {
		t.Fatalf("no file = defaults (sim): %+v %v", cfg, err)
	}
}

func TestOpenOutputs(t *testing.T) {
	cfg := defaultConfig()
	out, err := openOutputs(cfg, quiet())
	if err != nil || out.Name() != "sim" || out.Count() != 8 {
		t.Fatalf("sim: %v %v", out, err)
	}
	cfg.Outputs.Driver = "bogus"
	if _, err := openOutputs(cfg, quiet()); err == nil {
		t.Fatal("unknown driver must fail")
	}
	// Real drivers fail cleanly when the chip doesn't exist.
	cfg.Outputs.Chip = "gpiochip-does-not-exist"
	for _, d := range []string{"shiftreg", "gpiorelay"} {
		cfg.Outputs.Driver = d
		cfg.Outputs.Relay.Pins = []int{5}
		if _, err := openOutputs(cfg, quiet()); err == nil {
			t.Errorf("%s on a missing chip must fail", d)
		}
	}
}

// testCert writes a throwaway self-signed certificate for 127.0.0.1.
func testCert(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestTLSConfig(t *testing.T) {
	cfg := defaultConfig()
	cfg.TLS.Listen = ":443"
	if _, err := tlsConfig(cfg); err == nil {
		t.Fatal("listen without cert/key must fail: there is no built-in CA")
	}
	cfg.TLS.Cert, cfg.TLS.Key = testCert(t)
	tc, err := tlsConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(tc.Certificates) != 1 || tc.MinVersion != tls.VersionTLS12 {
		t.Fatalf("certs %d min %x", len(tc.Certificates), tc.MinVersion)
	}
	cfg.TLS.Cert, cfg.TLS.Key = "/nonexistent.crt", "/nonexistent.key"
	if _, err := tlsConfig(cfg); err == nil {
		t.Fatal("missing certificate must fail")
	}
}

func TestServerLimits(t *testing.T) {
	srv := newServer(nil, nil)
	if srv.ReadHeaderTimeout == 0 || srv.MaxHeaderBytes == 0 || srv.WriteTimeout != 0 {
		t.Fatal("server limits: header timeout and size set, no write timeout (SSE)")
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// TestServeStartsAndStops runs the whole controller on the sim driver:
// HTTP and HTTPS answer, and cancelling shuts down cleanly.
func TestServeStartsAndStops(t *testing.T) {
	dir := t.TempDir()
	httpAddr, httpsAddr := freePort(t), freePort(t)
	certPath, keyPath := testCert(t)
	conf := filepath.Join(dir, "c.toml")
	body := "listen = \"" + httpAddr + "\"\ndb = \"" + filepath.Join(dir, "s.db") + "\"\n" +
		"[outputs]\ndriver = \"sim\"\n[inputs.rain]\nsim = true\n[clock]\nwait_sync = false\n" +
		"[tls]\nlisten = \"" + httpsAddr + "\"\ncert = \"" + certPath + "\"\nkey = \"" + keyPath + "\"\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, []string{"-config", conf}, quiet()) }()

	get := func(c *http.Client, url string) (int, error) {
		resp, err := c.Get(url)
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		return resp.StatusCode, nil
	}
	insecure := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // throwaway self-signed cert
	}}
	plain := &http.Client{Timeout: 2 * time.Second}
	var code int
	var err error
	for range 50 {
		if code, err = get(plain, "http://"+httpAddr+"/api/state"); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		cancel()
		t.Fatalf("http never answered: %v (serve: %v)", err, <-done)
	}
	if code != http.StatusOK && code != http.StatusUnauthorized {
		t.Errorf("GET /api/state over HTTP: %d", code)
	}
	if code, err := get(insecure, "https://"+httpsAddr+"/api/state"); err != nil || (code != http.StatusOK && code != http.StatusUnauthorized) {
		t.Errorf("GET /api/state over HTTPS: %d %v", code, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop")
	}
}

func TestSdNotifyAndWatchdog(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "notify")
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		t.Skip("unixgram sockets unavailable:", err)
	}
	defer c.Close()
	read := func() string {
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		b := make([]byte, 64)
		n, err := c.Read(b)
		if err != nil {
			return ""
		}
		return string(b[:n])
	}

	t.Setenv("NOTIFY_SOCKET", "")
	sdNotify("READY=1") // no socket: must be a no-op
	t.Setenv("WATCHDOG_USEC", "")
	if watchdog(quiet()) != nil {
		t.Fatal("no watchdog without WATCHDOG_USEC")
	}

	t.Setenv("NOTIFY_SOCKET", sock)
	sdNotify("READY=1")
	if got := read(); got != "READY=1" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("WATCHDOG_USEC", "30000000")
	ping := watchdog(quiet())
	ping()
	ping() // within 5 s: rate-limited, no second message
	if got := read(); got != "WATCHDOG=1" {
		t.Fatalf("got %q", got)
	}
	if got := read(); got != "" {
		t.Fatalf("watchdog not rate-limited, got %q", got)
	}
}

type countOff struct{ n int }

func (c *countOff) AllOff() error { c.n++; return nil }

func TestAllOffOnPanic(t *testing.T) {
	out := &countOff{}
	var got any
	func() {
		defer func() { got = recover() }()
		func() {
			defer allOffOnPanic(out)
			panic("boom")
		}()
	}()
	if got != "boom" || out.n != 1 {
		t.Fatalf("panic %v, all-off calls %d; want boom, 1", got, out.n)
	}
	func() {
		defer allOffOnPanic(out)
	}()
	if out.n != 1 {
		t.Fatalf("all-off without a panic")
	}
}
