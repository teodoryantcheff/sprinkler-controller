package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sprinkler/internal/core"
)

func TestMigration2TightensDefaultCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		migrations[0],
		"PRAGMA user_version = 1",
		"INSERT INTO zones(id,output_index,name,max_minutes) VALUES(1,0,'Lawn',120),(2,1,'Hedge',120),(3,2,'Pots',30)",
		`INSERT INTO programs(id,name,day_rule,start_rule) VALUES(1,'Long','{}','{}')`,
		"INSERT INTO program_zones(program_id,zone_id,seconds,sort_order) VALUES(1,1,1200,0),(1,2,4200,1)",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	db.Close()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	zs, err := st.Zones()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, z := range zs {
		got[z.Name] = z.MaxMinutes
	}
	// Lawn drops to 60; Hedge keeps 120 (a 70 min program run); Pots was set by hand.
	if got["Lawn"] != 60 || got["Hedge"] != 120 || got["Pots"] != 30 {
		t.Fatalf("caps after migration: %v", got)
	}
}

func TestSettingsSurviveOneBadValue(t *testing.T) {
	st := open(t)
	s := core.DefaultSettings()
	s.StationDelayS = 10
	if err := st.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	// A wrong type for one key (still valid JSON) must not stop start-up.
	if err := st.SetRawSetting("lat", `"north"`); err != nil {
		t.Fatal(err)
	}
	got, err := st.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if got.StationDelayS != 10 || got.Lat != core.DefaultSettings().Lat {
		t.Fatalf("settings: delay %d lat %v", got.StationDelayS, got.Lat)
	}
}

func TestSkips(t *testing.T) {
	st := open(t)
	at := time.Date(2026, 11, 10, 6, 0, 0, 0, sofia)
	if err := st.SetSkip(1, at, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSkip(1, at, true); err != nil { // idempotent
		t.Fatal(err)
	}
	sk, err := st.Skips(at.Add(-time.Hour))
	if err != nil || !sk[SkipKey{1, at.Unix()}] || len(sk) != 1 {
		t.Fatalf("skips %v %v", sk, err)
	}
	if sk, _ := st.Skips(at.Add(time.Second)); len(sk) != 0 {
		t.Fatalf("skips after the start: %v", sk)
	}
	if _, _, err := st.Prune(at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if sk, _ := st.Skips(time.Time{}); len(sk) != 0 {
		t.Fatalf("prune kept old skips: %v", sk)
	}
	_ = st.SetSkip(1, at, true)
	_ = st.SetSkip(1, at, false)
	if sk, _ := st.Skips(time.Time{}); len(sk) != 0 {
		t.Fatalf("unskip: %v", sk)
	}
}

func TestSnapshotAndRestore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.db")
	snaps := filepath.Join(dir, "snapshots")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	addZones(t, st, "Lawn")
	for _, day := range []string{"2026-11-07", "2026-11-08", "2026-11-09"} {
		if _, err := st.Snapshot(snaps, day, 2); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := Snapshots(snaps)
	if len(list) != 2 || filepath.Base(list[0]) != "sprinkler-2026-11-09.db" {
		t.Fatalf("kept snapshots: %v", list)
	}
	if p, at, err := LatestSnapshot(snaps); err != nil || p != list[0] || at.IsZero() {
		t.Fatalf("latest: %q %v %v", p, at, err)
	}
	z := core.Zone{OutputIndex: 5, Name: "Later", Enabled: true, GroupID: 1}
	if err := st.SaveZone(&z); err != nil { // after the snapshot: lost on restore
		t.Fatal(err)
	}
	st.Close()

	// Damage the file: overwrite pages after the header.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 4096; i < len(b) && i < 4*4096; i++ {
		b[i] = 0xA5
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(path); !errors.Is(err, ErrCorrupt) {
		if s != nil {
			s.Close()
		}
		t.Fatalf("open damaged db: %v", err)
	}
	used, err := RestoreSnapshot(path, snaps, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))
	if err != nil || filepath.Base(used) != "sprinkler-2026-11-09.db" {
		t.Fatalf("restore: %q %v", used, err)
	}
	if _, err := os.Stat(path + ".corrupt-20261109T120000Z"); err != nil {
		t.Fatalf("damaged file not kept aside: %v", err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	zs, _ := st.Zones()
	if len(zs) != 1 || zs[0].Name != "Lawn" {
		t.Fatalf("restored zones: %v", zs)
	}
	if _, err := RestoreSnapshot(path, filepath.Join(dir, "none"), time.Now()); err == nil {
		t.Fatal("restore without snapshots must fail")
	}
}
