package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"sprinkler/internal/store"
)

// errBusy makes `sprinkler busy` exit with status 3.
var errBusy = errors.New("busy")

// busyCmd reports whether the controller is watering or has runs queued,
// by reading the database read-only (no migrations), so a newly copied
// binary can check the live database before it replaces the running one.
func busyCmd(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("busy", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "TOML config (to find the database)")
	dbPath := fs.String("db", "", "database file (overrides the config)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*cfgPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	if *dbPath != "" {
		cfg.DB = *dbPath
	}
	st, err := store.OpenReadOnly(cfg.DB)
	if err != nil {
		return fmt.Errorf("open %s read-only: %w", cfg.DB, err)
	}
	defer st.Close()
	n, err := st.ActiveRuns()
	if err != nil {
		return err
	}
	if n > 0 {
		fmt.Fprintf(out, "busy: %d run(s) watering or queued\n", n)
		return errBusy
	}
	fmt.Fprintln(out, "idle: nothing watering or queued")
	return nil
}
