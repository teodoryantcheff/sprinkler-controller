package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sprinkler/internal/core"
	"sprinkler/internal/store"
)

func TestBusy(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	z := core.Zone{Name: "Lawn", Enabled: true}
	if err := st.SaveZone(&z); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := busyCmd([]string{"-db", db}, &out); err != nil || !strings.HasPrefix(out.String(), "idle") {
		t.Fatalf("idle DB: %v %q", err, out.String())
	}

	r := core.Run{ZoneID: z.ID, PlannedStart: time.Now()} // queued: no outcome yet
	if err := st.InsertRun(&r); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := busyCmd([]string{"-db", db}, &out); !errors.Is(err, errBusy) || !strings.Contains(out.String(), "1 run") {
		t.Fatalf("queued run: %v %q", err, out.String())
	}

	r.Outcome = core.OutcomeDone
	if err := st.UpdateRun(&r); err != nil {
		t.Fatal(err)
	}
	st.Close()
	out.Reset()
	if err := busyCmd([]string{"-db", db}, &out); err != nil {
		t.Fatalf("finished run: %v %q", err, out.String())
	}

	// Config path works, and a missing database is an error, not "idle"
	// (read-only open must never create one).
	conf := filepath.Join(t.TempDir(), "c.toml")
	missing := filepath.Join(t.TempDir(), "nope.db")
	if err := os.WriteFile(conf, []byte("db = \""+missing+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := busyCmd([]string{"-config", conf}, &out); err == nil {
		t.Fatal("missing database must fail")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("busy must not create a database")
	}
}

func TestOpenReadOnlyDoesNotMigrate(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec("PRAGMA user_version = 1"); err != nil { // pretend: an older schema
		t.Fatal(err)
	}
	st.Close()
	ro, err := store.OpenReadOnly(db)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	var v int
	if err := ro.DB.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 1 {
		t.Fatalf("read-only open changed the schema version: %d %v", v, err)
	}
	if _, err := ro.DB.Exec("DELETE FROM runs"); err == nil {
		t.Fatal("read-only store accepted a write")
	}
}
