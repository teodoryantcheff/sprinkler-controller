package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sprinkler/internal/core"
	"sprinkler/internal/store"
)

func TestSkipPlannedStart(t *testing.T) {
	a, sim, st := setup(t)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, sofia)
	a.Clock = func() time.Time { return now }
	tomorrow := time.Date(2026, 11, 8, 6, 0, 0, 0, sofia)

	// Only real future starts can be skipped.
	for _, bad := range []time.Time{tomorrow.Add(time.Minute), time.Date(2026, 11, 7, 6, 0, 0, 0, sofia)} {
		if err := a.SetSkip(1, bad, true); !errors.Is(err, ErrNotPlanned) {
			t.Errorf("skip %v: %v", bad, err)
		}
	}
	if err := a.SetSkip(99, tomorrow, true); !errors.Is(err, ErrNotPlanned) {
		t.Errorf("unknown program: %v", err)
	}
	if err := a.SetSkip(1, tomorrow, true); err != nil {
		t.Fatal(err)
	}

	up := a.Upcoming(now, now.Add(48*time.Hour), 0)
	var skipped, planned int
	for _, r := range up {
		switch r.Skip {
		case core.OutcomeSkippedUser:
			skipped++
			if !r.PlannedStart.Equal(tomorrow) {
				t.Errorf("skipped run at %v", r.PlannedStart)
			}
		case "":
			planned++
		}
	}
	if skipped != 2 || planned != 2 {
		t.Fatalf("upcoming: %d skipped, %d planned (%v)", skipped, planned, up)
	}

	// At the start: nothing waters, the zone runs are logged as skipped.
	now = tomorrow
	a.lastPlan = now.Add(-time.Second)
	a.PlanTick(now)
	a.Runner.Tick(now)
	if sim.States()[0] {
		t.Fatal("skipped start must not water")
	}
	runs, _ := st.Runs(store.RunFilter{Outcome: core.OutcomeSkippedUser})
	if len(runs) != 2 {
		t.Fatalf("want 2 skipped_user runs, got %d", len(runs))
	}
	if open, _ := st.OpenRuns(); len(open) != 0 {
		t.Fatalf("queued after skip: %v", open)
	}

	// Un-skip restores the next day's start (a different one, never skipped).
	day3 := tomorrow.AddDate(0, 0, 1)
	if err := a.SetSkip(1, day3, true); err != nil {
		t.Fatal(err)
	}
	if err := a.SetSkip(1, day3, false); err != nil {
		t.Fatal(err)
	}
	if sk, _ := st.Skips(now.Add(time.Second)); len(sk) != 0 {
		t.Fatalf("skips left: %v", sk)
	}
}

func TestSnapshotTick(t *testing.T) {
	a, _, _ := setup(t)
	dir := filepath.Join(t.TempDir(), "snaps")
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, sofia)

	a.SnapshotTick(now) // off: no dir
	if a.SnapshotStatus().Enabled {
		t.Fatal("snapshots should be off")
	}
	a.SnapshotDir, a.SnapshotKeep = dir, 2
	a.SnapshotTick(now) // clock not trusted yet
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("snapshot before the clock synced")
	}
	a.clockOK.Store(true)

	// Not while watering.
	if _, err := a.RunProgramNow(1); err != nil {
		t.Fatal(err)
	}
	a.Runner.Tick(now)
	a.SnapshotTick(now)
	if s := a.SnapshotStatus(); !s.Last.IsZero() {
		t.Fatalf("snapshot while watering: %+v", s)
	}
	a.Runner.StopAll(now)

	a.SnapshotTick(now)
	s := a.SnapshotStatus()
	if s.Last.IsZero() || s.Error != "" {
		t.Fatalf("status after snapshot: %+v", s)
	}
	if _, err := os.Stat(filepath.Join(dir, "sprinkler-2026-11-07.db")); err != nil {
		t.Fatal(err)
	}
	// Once a day: a second tick the same day does nothing, the next day adds one.
	a.SnapshotTick(now.Add(time.Hour))
	a.SnapshotTick(now.AddDate(0, 0, 1))
	a.SnapshotTick(now.AddDate(0, 0, 2))
	list, _ := store.Snapshots(dir)
	if len(list) != 2 {
		t.Fatalf("kept: %v", list)
	}
}
