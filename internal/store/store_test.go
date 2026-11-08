package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"sprinkler/internal/core"
)

var sofia, _ = time.LoadLocation("Europe/Sofia")

func open(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func addZones(t *testing.T, st *Store, names ...string) []core.Zone {
	t.Helper()
	var out []core.Zone
	for i, n := range names {
		z := core.Zone{OutputIndex: i, Name: n, Enabled: true, GroupID: 1}
		if err := st.SaveZone(&z); err != nil {
			t.Fatal(err)
		}
		out = append(out, z)
	}
	return out
}

func program(zs ...core.Zone) core.Program {
	p := core.Program{
		Name: "Morning", Enabled: true, WeatherScaled: true,
		DayRule:   core.DayRule{Kind: "interval", Every: 2, Anchor: "2026-01-01"},
		StartRule: core.StartRule{Kind: "fixed", Times: []string{"06:00", "18:30"}},
	}
	for i, z := range zs {
		p.Zones = append(p.Zones, core.ProgramZone{ZoneID: z.ID, Seconds: 300 * (i + 1)})
	}
	return p
}

func TestReopenKeepsDataAndMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	addZones(t, st, "Lawn")
	st.Close()
	st, err = Open(path) // migrations must not run twice
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	zs, err := st.Zones()
	if err != nil || len(zs) != 1 || zs[0].Name != "Lawn" {
		t.Fatalf("zones after reopen: %v %v", zs, err)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	st := open(t)
	s, err := st.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Timezone != core.DefaultSettings().Timezone || s.WateringLevelPct != core.DefaultSettings().WateringLevelPct {
		t.Fatalf("empty DB should give defaults, got %+v", s)
	}
	s.WateringLevelPct = 75
	s.StationDelayS = 10
	until := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	s.RainDelayUntil = &until
	if err := st.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	// A raw secret and an unknown key share the table and must not break
	// the typed load.
	if err := st.SetRawSetting("session_secret", "abc"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if got.WateringLevelPct != 75 || got.StationDelayS != 10 || got.RainDelayUntil == nil || !got.RainDelayUntil.Equal(until) {
		t.Fatalf("settings round trip: %+v", got)
	}
	if v, _ := st.RawSetting("session_secret"); v != "abc" {
		t.Fatalf("raw setting %q", v)
	}
	if v, err := st.RawSetting("missing"); v != "" || err != nil {
		t.Fatalf("missing raw setting: %q %v", v, err)
	}
}

func TestZones(t *testing.T) {
	st := open(t)
	zs := addZones(t, st, "Lawn", "Hedge")
	if zs[0].MaxMinutes != 120 {
		t.Errorf("default max minutes %d", zs[0].MaxMinutes)
	}
	z := zs[1]
	z.Name, z.IgnoreRain, z.Enabled, z.SortOrder = "Hedges", true, false, -1
	if err := st.SaveZone(&z); err != nil {
		t.Fatal(err)
	}
	got, err := st.Zone(z.ID)
	if err != nil || got.Name != "Hedges" || !got.IgnoreRain || got.Enabled {
		t.Fatalf("zone update: %+v %v", got, err)
	}
	all, _ := st.Zones()
	if len(all) != 2 || all[0].ID != z.ID {
		t.Fatalf("zones should follow sort_order: %+v", all)
	}
}

func TestPrograms(t *testing.T) {
	st := open(t)
	zs := addZones(t, st, "Lawn", "Hedge")
	p := program(zs...)
	if err := st.SaveProgram(&p); err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 || p.SortOrder != 1 {
		t.Fatalf("insert: id %d sort %d", p.ID, p.SortOrder)
	}
	got, err := st.Program(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DayRule.Every != 2 || len(got.StartRule.Times) != 2 || len(got.Zones) != 2 || got.Zones[1].Seconds != 600 {
		t.Fatalf("program round trip: %+v", got)
	}
	// Update replaces the zone list, in the new order.
	p.Zones = []core.ProgramZone{{ZoneID: zs[1].ID, Seconds: 60}}
	p.Name = "Evening"
	if err := st.SaveProgram(&p); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Program(p.ID)
	if got.Name != "Evening" || len(got.Zones) != 1 || got.Zones[0].ZoneID != zs[1].ID {
		t.Fatalf("program update: %+v", got)
	}
	ghost := program(zs...)
	ghost.ID = 999
	if err := st.SaveProgram(&ghost); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("update of a missing program: %v", err)
	}
	bad := program(zs...)
	bad.StartRule.Times = []string{"25:00"}
	if err := st.SaveProgram(&bad); err == nil {
		t.Fatal("invalid program should be rejected")
	}
	if err := st.DeleteProgram(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Program(p.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted program: %v", err)
	}
}

func TestRuns(t *testing.T) {
	st := open(t)
	zs := addZones(t, st, "Lawn", "Hedge")
	p := program(zs...)
	if err := st.SaveProgram(&p); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 2, 6, 0, 0, 0, sofia) // a Monday
	start := day.Add(5 * time.Second)
	end := start.Add(5 * time.Minute)
	done := core.Run{ZoneID: zs[0].ID, ProgramID: &p.ID, PlannedStart: day, Start: &start, End: &end, PlannedSeconds: 300, ActualSeconds: 300, ScalePct: 100, Outcome: core.OutcomeDone}
	manual := core.Run{ZoneID: zs[1].ID, PlannedStart: day.Add(time.Hour), PlannedSeconds: 60, ScalePct: 100}
	for _, r := range []*core.Run{&done, &manual} {
		if err := st.InsertRun(r); err != nil {
			t.Fatal(err)
		}
	}
	if ok, _ := st.ProgramStarted(p.ID, day); !ok {
		t.Error("program start should be recorded")
	}
	if ok, _ := st.ProgramStarted(p.ID, day.Add(time.Minute)); ok {
		t.Error("other start should not be recorded")
	}
	open, _ := st.OpenRuns()
	if len(open) != 1 || open[0].ID != manual.ID || open[0].ProgramID != nil {
		t.Fatalf("open runs: %+v", open)
	}
	// Finish the manual run.
	ms := manual.PlannedStart
	me := ms.Add(time.Minute)
	manual.Start, manual.End, manual.ActualSeconds, manual.Outcome = &ms, &me, 60, core.OutcomeStopped
	if err := st.UpdateRun(&manual); err != nil {
		t.Fatal(err)
	}
	runs, _ := st.Runs(RunFilter{})
	if len(runs) != 2 || runs[0].ID != manual.ID {
		t.Fatalf("runs newest first: %+v", runs)
	}
	for name, f := range map[string]RunFilter{
		"zone":    {ZoneID: zs[0].ID},
		"program": {ProgramID: p.ID},
		"outcome": {Outcome: core.OutcomeDone},
		"before":  {Before: day.Add(time.Minute)},
	} {
		if rs, err := st.Runs(f); err != nil || len(rs) != 1 || rs[0].ID != done.ID {
			t.Errorf("%s filter: %+v %v", name, rs, err)
		}
	}
	if rs, _ := st.Runs(RunFilter{ProgramID: -1}); len(rs) != 1 || rs[0].ID != manual.ID {
		t.Errorf("manual-only filter: %+v", rs)
	}
	if rs, _ := st.Runs(RunFilter{After: day.Add(time.Minute)}); len(rs) != 1 || rs[0].ID != manual.ID {
		t.Errorf("after filter: %+v", rs)
	}
	secs, _ := st.ZoneSecondsSince(day.Add(-time.Hour))
	if secs[zs[0].ID] != 300 || secs[zs[1].ID] != 60 {
		t.Errorf("zone seconds: %v", secs)
	}
	weeks, _ := st.WeeklyMinutes(day.Add(-24*time.Hour), sofia)
	total := 0.0
	for _, w := range weeks {
		if w.Week != "2026-11-02" {
			t.Errorf("week start %s, want Monday 2026-11-02", w.Week)
		}
		total += w.Minutes
	}
	if total != 6 {
		t.Errorf("weekly minutes total %v, want 6", total)
	}
}

func TestEvents(t *testing.T) {
	st := open(t)
	t0 := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	if err := st.AddEvent(t0, "boot", map[string]any{"version": "x"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddEvent(t0.Add(time.Second), "rain_delay_ended", nil); err != nil {
		t.Fatal(err)
	}
	evs, err := st.Events(t0.Add(time.Hour), 10)
	if err != nil || len(evs) != 2 {
		t.Fatalf("events: %v %v", evs, err)
	}
	if evs[0].Kind != "rain_delay_ended" || string(evs[0].Detail) != "{}" || string(evs[1].Detail) != `{"version":"x"}` {
		t.Fatalf("events order/detail: %+v", evs)
	}
	if evs, _ := st.Events(t0.Add(time.Second), 10); len(evs) != 1 {
		t.Fatalf("before filter: %+v", evs)
	}
}

func TestPrune(t *testing.T) {
	st := open(t)
	zs := addZones(t, st, "Lawn")
	cut := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	old, newer := cut.Add(-time.Hour), cut.Add(time.Hour)
	runs := []core.Run{
		{ZoneID: zs[0].ID, PlannedStart: old, Outcome: core.OutcomeDone},
		{ZoneID: zs[0].ID, PlannedStart: old}, // still open: kept
		{ZoneID: zs[0].ID, PlannedStart: newer, Outcome: core.OutcomeDone},
	}
	for i := range runs {
		if err := st.InsertRun(&runs[i]); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.AddEvent(old, "boot", nil)
	_ = st.AddEvent(newer, "boot", nil)
	r, e, err := st.Prune(cut)
	if err != nil || r != 1 || e != 1 {
		t.Fatalf("prune: runs %d events %d err %v", r, e, err)
	}
	if open, _ := st.OpenRuns(); len(open) != 1 {
		t.Errorf("open run must survive: %+v", open)
	}
	if rs, _ := st.Runs(RunFilter{}); len(rs) != 1 || rs[0].ID != runs[2].ID {
		t.Errorf("newer run must survive: %+v", rs)
	}
	if evs, _ := st.Events(newer.Add(time.Hour), 10); len(evs) != 1 {
		t.Errorf("newer event must survive: %+v", evs)
	}
}

func TestUsers(t *testing.T) {
	st := open(t)
	if n, _ := st.UserCount(); n != 0 {
		t.Fatalf("users %d", n)
	}
	if err := st.SaveUser("admin", "h1"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveUser("admin", "h2"); err != nil { // update, not duplicate
		t.Fatal(err)
	}
	if n, _ := st.UserCount(); n != 1 {
		t.Fatalf("users %d", n)
	}
	u, err := st.UserByName("admin")
	if err != nil || u.PasswordHash != "h2" {
		t.Fatalf("user: %+v %v", u, err)
	}
	if _, err := st.UserByName("nobody"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing user: %v", err)
	}
}

func TestExportRestore(t *testing.T) {
	src := open(t)
	zs := addZones(t, src, "Lawn", "Hedge")
	p := program(zs...)
	if err := src.SaveProgram(&p); err != nil {
		t.Fatal(err)
	}
	s, _ := src.Settings()
	s.WateringLevelPct = 80
	if err := src.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	b, err := src.Export()
	if err != nil {
		t.Fatal(err)
	}

	dst := open(t)
	addZones(t, dst, "Old 1", "Old 2", "Old 3")
	hist := core.Run{ZoneID: 1, PlannedStart: time.Now(), Outcome: core.OutcomeDone}
	if err := dst.InsertRun(&hist); err != nil {
		t.Fatal(err)
	}
	if err := dst.Restore(context.Background(), b, 8); err != nil {
		t.Fatal(err)
	}
	gz, _ := dst.Zones()
	gp, _ := dst.Programs()
	gs, _ := dst.Settings()
	if len(gz) != 2 || gz[1].Name != "Hedge" || len(gp) != 1 || len(gp[0].Zones) != 2 || gs.WateringLevelPct != 80 {
		t.Fatalf("restored: zones %+v programs %+v level %d", gz, gp, gs.WateringLevelPct)
	}
	if rs, _ := dst.Runs(RunFilter{}); len(rs) != 1 {
		t.Errorf("restore must keep history, got %d runs", len(rs))
	}

	// Invalid backups are refused and change nothing.
	bad := b
	bad.Programs = []core.Program{program(core.Zone{ID: 42})}
	if err := dst.Restore(context.Background(), bad, 8); err == nil {
		t.Fatal("program with unknown zone should be refused")
	}
	bad = b
	bad.Version = 2
	if err := dst.Restore(context.Background(), bad, 8); err == nil {
		t.Fatal("unknown version should be refused")
	}
	bad = b
	bad.Settings.MasterZoneID = zs[0].ID
	if err := dst.Restore(context.Background(), bad, 8); err == nil {
		t.Fatal("master valve inside a program should be refused")
	}
	if err := dst.Restore(context.Background(), b, 1); err == nil {
		t.Fatal("zone on an output the driver lacks should be refused")
	}
	if gz, _ := dst.Zones(); len(gz) != 2 {
		t.Fatalf("failed restore changed zones: %+v", gz)
	}
}
