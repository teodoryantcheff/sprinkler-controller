package app

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"sprinkler/internal/core"
	"sprinkler/internal/hw"
	"sprinkler/internal/runner"
	"sprinkler/internal/store"
)

var sofia, _ = time.LoadLocation("Europe/Sofia")

func setup(t *testing.T) (*App, *hw.Sim, *store.Store) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for i, n := range []string{"Lawn", "Hedge"} {
		z := core.Zone{OutputIndex: i, Name: n, Enabled: true, GroupID: 1, MaxMinutes: 120}
		if err := st.SaveZone(&z); err != nil {
			t.Fatal(err)
		}
	}
	p := core.Program{
		Name: "Morning", Enabled: true, WeatherScaled: true,
		DayRule:   core.DayRule{Kind: "interval", Every: 1, Anchor: "2026-01-01"},
		StartRule: core.StartRule{Kind: "fixed", Times: []string{"06:00"}},
		Zones:     []core.ProgramZone{{ZoneID: 1, Seconds: 600}, {ZoneID: 2, Seconds: 300}},
	}
	if err := st.SaveProgram(&p); err != nil {
		t.Fatal(err)
	}
	s := core.DefaultSettings()
	s.StationDelayS = 10
	s.Sensor = core.SensorConfig{Enabled: true, Kind: "rain", Normally: "open", OnDelayS: 5}
	if err := st.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	sim := hw.NewSim(8, log)
	r, err := runner.New(sim, st, sofia, log)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(st, r, log)
	if err != nil {
		t.Fatal(err)
	}
	a.SimSensor = &hw.SimInput{}
	return a, sim, st
}

func TestPlanQueuesOnceAndRuns(t *testing.T) {
	a, sim, st := setup(t)
	now := time.Date(2026, 11, 7, 5, 59, 50, 0, sofia)
	for i := 0; i < 30; i++ { // 05:59:50 .. 06:00:19
		a.PlanTick(now)
		a.Runner.Tick(now)
		now = now.Add(time.Second)
	}
	if !sim.States()[0] {
		t.Fatal("lawn should be watering at 06:00:19")
	}
	runs, _ := st.Runs(store.RunFilter{Limit: 10})
	open, _ := st.OpenRuns()
	if len(runs) != 0 || len(open) != 2 {
		t.Fatalf("want 2 open runs (running + queued), got finished=%d open=%d", len(runs), len(open))
	}
	// Restart inside the same minute must not queue again.
	a.lastPlan = time.Time{}
	a.PlanTick(time.Date(2026, 11, 7, 6, 0, 40, 0, sofia))
	open, _ = st.OpenRuns()
	if len(open) != 2 {
		t.Fatalf("duplicate start after restart: %d open runs", len(open))
	}
}

func TestRainDelaySkips(t *testing.T) {
	a, sim, st := setup(t)
	now := time.Date(2026, 11, 7, 5, 0, 0, 0, sofia)
	a.Clock = func() time.Time { return now }
	if err := a.SetRainDelay(24); err != nil {
		t.Fatal(err)
	}
	now = time.Date(2026, 11, 7, 6, 0, 0, 0, sofia)
	a.lastPlan = now.Add(-time.Second)
	a.PlanTick(now)
	a.Runner.Tick(now)
	if sim.States()[0] {
		t.Fatal("must not water during rain delay")
	}
	runs, _ := st.Runs(store.RunFilter{Outcome: core.OutcomeSkippedRain})
	if len(runs) != 2 {
		t.Fatalf("want 2 skipped_rain runs, got %d", len(runs))
	}
}

func TestSensorOnDelayAndBlock(t *testing.T) {
	a, sim, _ := setup(t)
	now := time.Date(2026, 11, 7, 5, 59, 59, 0, sofia)
	a.PlanTick(now)
	now = now.Add(time.Second)
	a.PlanTick(now)
	a.Runner.Tick(now)
	if !sim.States()[0] {
		t.Fatal("should water")
	}
	a.SimSensor.SetWet(true)
	for i := 0; i < 4; i++ {
		now = now.Add(time.Second)
		a.SensorTick(now)
		a.Runner.Tick(now)
	}
	if !sim.States()[0] {
		t.Fatal("on-delay not respected")
	}
	for i := 0; i < 2; i++ {
		now = now.Add(time.Second)
		a.SensorTick(now)
		a.Runner.Tick(now)
	}
	if sim.States()[0] {
		t.Fatal("sensor wet: scheduled run must stop")
	}
	if !a.SensorState().Wet {
		t.Error("sensor should read wet")
	}
}

func TestRecoverClosesOpenRuns(t *testing.T) {
	a, _, st := setup(t)
	start := time.Date(2026, 11, 7, 6, 0, 0, 0, sofia).UTC()
	r := core.Run{ZoneID: 1, PlannedStart: start, Start: &start, PlannedSeconds: 600, ActualSeconds: 90, ScalePct: 100}
	if err := st.InsertRun(&r); err != nil {
		t.Fatal(err)
	}
	if err := a.Recover(start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	runs, _ := st.Runs(store.RunFilter{Outcome: core.OutcomeError})
	if len(runs) != 1 || runs[0].End == nil || runs[0].End.Sub(start) != 90*time.Second {
		t.Fatalf("%+v", runs)
	}
}

func TestUpcoming(t *testing.T) {
	a, _, _ := setup(t)
	from := time.Date(2026, 11, 7, 12, 0, 0, 0, sofia)
	up := a.Upcoming(from, from.Add(48*time.Hour), 3)
	if len(up) != 3 {
		t.Fatalf("%+v", up)
	}
	if up[0].EstStart.In(sofia).Format("01-02 15:04:05") != "11-08 06:00:00" ||
		up[1].EstStart.In(sofia).Format("15:04:05") != "06:10:10" {
		t.Errorf("%v %v", up[0].EstStart, up[1].EstStart)
	}
}
