package main

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sprinkler/internal/core"
	"sprinkler/internal/store"
)

func TestOpenStoreRecovers(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	db := filepath.Join(dir, "s.db")
	snaps := filepath.Join(dir, "snapshots")

	st, restored, err := openStore(db, snaps, time.Now(), log)
	if err != nil || restored != "" {
		t.Fatalf("fresh: %q %v", restored, err)
	}
	z := core.Zone{OutputIndex: 0, Name: "Lawn", Enabled: true, GroupID: 1}
	if err := st.SaveZone(&z); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Snapshot(snaps, "2026-11-09", 7); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// An empty file (torn create) with a snapshot around: restore it.
	if err := os.WriteFile(db, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(db + "-wal")
	_ = os.Remove(db + "-shm")
	st, restored, err = openStore(db, snaps, time.Now(), log)
	if err != nil || filepath.Base(restored) != "sprinkler-2026-11-09.db" {
		t.Fatalf("empty file: %q %v", restored, err)
	}
	zs, _ := st.Zones()
	st.Close()
	if len(zs) != 1 {
		t.Fatalf("restored zones: %v", zs)
	}

	// Garbage: restore again.
	if err := os.WriteFile(db, []byte("not a database, just garbage bytes ......................................................................................"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, restored, err = openStore(db, snaps, time.Now().Add(time.Second), log)
	if err != nil || restored == "" {
		t.Fatalf("garbage: %q %v", restored, err)
	}
	st.Close()

	// Damaged and no snapshot: fail, never start empty.
	if err := os.WriteFile(db, []byte("garbage garbage garbage garbage garbage garbage garbage garbage garbage garbage garbage garbage garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openStore(db, filepath.Join(dir, "none"), time.Now(), log); !errors.Is(err, store.ErrCorrupt) {
		t.Fatalf("no snapshot: %v", err)
	}
	if _, _, err := openStore(db, "", time.Now(), log); !errors.Is(err, store.ErrCorrupt) {
		t.Fatalf("snapshots off: %v", err)
	}
}
