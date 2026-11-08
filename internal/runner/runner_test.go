package runner

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"sprinkler/internal/core"
	"sprinkler/internal/hw"
)

type memStore struct {
	mu     sync.Mutex
	next   int64
	runs   map[int64]core.Run
	events []string
}

func (m *memStore) InsertRun(r *core.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	r.ID = m.next
	m.runs[r.ID] = *r
	return nil
}

func (m *memStore) UpdateRun(r *core.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[r.ID] = *r
	return nil
}

func (m *memStore) AddEvent(t time.Time, kind string, _ any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, kind)
	return nil
}

var (
	sofia, _ = time.LoadLocation("Europe/Sofia")
	t0       = time.Date(2026, 11, 7, 6, 0, 0, 0, sofia)
)

type rig struct {
	t   *testing.T
	r   *Runner
	sim *hw.Sim
	st  *memStore
	now time.Time
}

func newRig(t *testing.T, mutate func(*core.Settings, []core.Zone)) *rig {
	t.Helper()
	sim := hw.NewSim(8, slog.New(slog.NewTextHandler(io.Discard, nil)))
	st := &memStore{runs: map[int64]core.Run{}}
	r, err := New(sim, st, sofia, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	zones := []core.Zone{
		{ID: 1, OutputIndex: 0, Name: "A", Enabled: true, GroupID: 1, MaxMinutes: 120},
		{ID: 2, OutputIndex: 1, Name: "B", Enabled: true, GroupID: 1, MaxMinutes: 120},
		{ID: 3, OutputIndex: 2, Name: "C", Enabled: true, GroupID: 0, MaxMinutes: 120},
		{ID: 4, OutputIndex: 3, Name: "D", Enabled: true, GroupID: 0, MaxMinutes: 120},
		{ID: 5, OutputIndex: 4, Name: "E", Enabled: true, GroupID: 0, MaxMinutes: 120},
		{ID: 8, OutputIndex: 7, Name: "Master", Enabled: true, GroupID: 0, MaxMinutes: 120},
	}
	s := core.DefaultSettings()
	s.StationDelayS = 10
	s.MaxOpenZones = 2
	if mutate != nil {
		mutate(&s, zones)
	}
	r.Configure(zones, s)
	return &rig{t: t, r: r, sim: sim, st: st, now: t0}
}

// advance ticks second by second.
func (g *rig) advance(secs int) {
	for i := 0; i < secs; i++ {
		g.now = g.now.Add(time.Second)
		g.r.Tick(g.now)
	}
}

func (g *rig) outputs() string {
	var b strings.Builder
	for _, v := range g.sim.States() {
		if v {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

func (g *rig) want(out string) {
	g.t.Helper()
	if got := g.outputs(); got != out {
		g.t.Fatalf("at +%v outputs %s, want %s", g.now.Sub(t0), got, out)
	}
}

func (g *rig) enqueue(reqs ...Request) []int64 {
	g.t.Helper()
	ids, err := g.r.Enqueue(g.now, reqs)
	if err != nil {
		g.t.Fatal(err)
	}
	return ids
}

func pid(v int64) *int64 { return &v }

func TestSequentialGroupWithDelay(t *testing.T) {
	g := newRig(t, nil)
	ids := g.enqueue(Request{ZoneID: 1, Seconds: 60, ProgramID: pid(1)}, Request{ZoneID: 2, Seconds: 30, ProgramID: pid(1)})
	g.advance(1)
	g.want("10000000")
	g.advance(59)
	g.want("00000000") // A done at 60 s
	g.advance(9)
	g.want("00000000") // station delay
	g.advance(1)
	g.want("01000000")
	g.advance(30)
	g.want("00000000")
	for _, id := range ids {
		r := g.st.runs[id]
		if r.Outcome != core.OutcomeDone {
			t.Errorf("run %d outcome %q", id, r.Outcome)
		}
	}
	if a := g.st.runs[ids[0]].ActualSeconds; a != 60 {
		t.Errorf("A watered %d s", a)
	}
}

func TestParallelLanesAndMaxOpen(t *testing.T) {
	g := newRig(t, nil)
	g.enqueue(Request{ZoneID: 3, Seconds: 60}, Request{ZoneID: 4, Seconds: 60}, Request{ZoneID: 5, Seconds: 60})
	g.advance(1)
	g.want("00110000") // max 2 open
	g.advance(60)
	g.want("00001000")
}

func TestManualPreemptsScheduled(t *testing.T) {
	g := newRig(t, nil)
	ids := g.enqueue(Request{ZoneID: 1, Seconds: 100, ProgramID: pid(1)})
	g.advance(40)
	g.want("10000000")
	man := g.enqueue(Request{ZoneID: 2, Seconds: 20, Manual: true})
	g.advance(1)
	g.want("01000000") // scheduled A paused, manual B runs
	g.advance(20)
	g.want("00000000") // delay after the manual run
	g.advance(10)
	g.want("10000000") // A resumes
	g.advance(60)
	g.want("00000000")
	if r := g.st.runs[ids[0]]; r.Outcome != core.OutcomeDone || r.ActualSeconds != 100 {
		t.Errorf("scheduled run %+v", r)
	}
	if r := g.st.runs[man[0]]; r.Outcome != core.OutcomeDone || r.ActualSeconds != 20 {
		t.Errorf("manual run %+v", r)
	}
	if _, err := g.r.Enqueue(g.now, []Request{{ZoneID: 99, Seconds: 5, Manual: true}}); !errors.Is(err, ErrUnknownZone) {
		t.Errorf("unknown zone: %v", err)
	}
}

func TestPauseResume(t *testing.T) {
	g := newRig(t, nil)
	id := g.enqueue(Request{ZoneID: 3, Seconds: 60})[0]
	g.advance(30)
	g.r.Pause(g.now)
	g.want("00000000")
	g.advance(100)
	g.want("00000000")
	g.r.Resume(g.now)
	g.advance(1)
	g.want("00100000")
	g.advance(29)
	g.want("00000000")
	if r := g.st.runs[id]; r.ActualSeconds != 60 || r.Outcome != core.OutcomeDone {
		t.Errorf("%+v", r)
	}
}

func TestStopAll(t *testing.T) {
	g := newRig(t, nil)
	ids := g.enqueue(Request{ZoneID: 1, Seconds: 60}, Request{ZoneID: 2, Seconds: 60}, Request{ZoneID: 3, Seconds: 60})
	g.advance(5)
	g.want("10100000")
	g.r.StopAll(g.now)
	g.want("00000000")
	for _, id := range ids {
		if g.st.runs[id].Outcome != core.OutcomeStopped {
			t.Errorf("run %d: %+v", id, g.st.runs[id])
		}
	}
	if g.st.runs[ids[1]].Start != nil {
		t.Error("queued run must not get a start time")
	}
}

func TestMaxMinutesCap(t *testing.T) {
	g := newRig(t, func(s *core.Settings, z []core.Zone) { z[2].MaxMinutes = 1 })
	id := g.enqueue(Request{ZoneID: 3, Seconds: 600})[0]
	g.advance(61)
	g.want("00000000")
	if r := g.st.runs[id]; r.Outcome != core.OutcomeCutMax || r.ActualSeconds != 60 {
		t.Errorf("%+v", r)
	}
}

func TestMaxMinutesLoweredMidRun(t *testing.T) {
	var zones []core.Zone
	var settings core.Settings
	g := newRig(t, func(s *core.Settings, z []core.Zone) { zones, settings = z, *s })
	id := g.enqueue(Request{ZoneID: 3, Seconds: 600})[0]
	g.advance(30)
	g.want("00100000")
	zones[2].MaxMinutes = 1
	g.r.Configure(zones, settings)
	g.advance(31)
	g.want("00000000")
	if r := g.st.runs[id]; r.Outcome != core.OutcomeCutMax || r.ActualSeconds != 60 {
		t.Errorf("%+v", r)
	}
}

func TestDailyCap(t *testing.T) {
	g := newRig(t, func(s *core.Settings, z []core.Zone) { s.DailyCapMinutes = 2 })
	g.r.SetDayUsage("2026-11-07", map[int64]int{3: 90})
	id := g.enqueue(Request{ZoneID: 3, Seconds: 600})[0]
	g.advance(40)
	g.want("00000000")
	if r := g.st.runs[id]; r.Outcome != core.OutcomeCutMax || r.ActualSeconds != 30 {
		t.Errorf("%+v", r)
	}
}

func TestOutputFailureFaults(t *testing.T) {
	g := newRig(t, nil)
	g.sim.Fail = 1 // one failure: the retry succeeds
	g.enqueue(Request{ZoneID: 3, Seconds: 60})
	g.advance(1)
	g.want("00100000")
	g.sim.Fail = 2 // two failures: fault, all off
	g.enqueue(Request{ZoneID: 4, Seconds: 60})
	g.advance(1)
	g.want("00000000")
	st := g.r.Snapshot(g.now)
	if st.Fault == "" || len(st.Running) != 0 || len(st.Queue) != 0 {
		t.Errorf("fault state: %+v", st)
	}
	if _, err := g.r.Enqueue(g.now, []Request{{ZoneID: 3, Seconds: 5}}); !errors.Is(err, ErrFault) {
		t.Errorf("enqueue during fault: %v", err)
	}
	g.r.ClearFault(g.now)
	g.enqueue(Request{ZoneID: 3, Seconds: 5})
	g.advance(11) // the faulted lane honours the station delay
	g.want("00100000")
}

func TestMasterValveDelays(t *testing.T) {
	g := newRig(t, func(s *core.Settings, z []core.Zone) {
		s.MasterZoneID = 8
		s.MasterOnDelayS = 5
		s.MasterOffDelayS = 10
		s.StationDelayS = 0
		z[0].UsesMaster = true
		z[1].UsesMaster = true
	})
	g.enqueue(Request{ZoneID: 1, Seconds: 20}, Request{ZoneID: 2, Seconds: 20})
	g.advance(1)
	g.want("00000001") // master first
	g.advance(5)
	g.want("10000001") // zone after on-delay
	g.advance(20)
	g.want("01000001") // master stays on between zones
	g.advance(20)
	g.want("00000001") // off-delay
	g.advance(10)
	g.want("00000000")
	if _, err := g.r.Enqueue(g.now, []Request{{ZoneID: 8, Seconds: 5, Manual: true}}); !errors.Is(err, ErrMasterZone) {
		t.Errorf("running the master as a zone: %v", err)
	}
}

func TestBlockStopsScheduledNotManual(t *testing.T) {
	g := newRig(t, func(s *core.Settings, z []core.Zone) {
		z[3].IgnoreSensor = true
		s.MaxOpenZones = 0 // unlimited
	})
	ids := g.enqueue(Request{ZoneID: 3, Seconds: 60, ProgramID: pid(1)}, Request{ZoneID: 4, Seconds: 60, ProgramID: pid(1)})
	g.enqueue(Request{ZoneID: 1, Seconds: 60, Manual: true})
	g.advance(1)
	g.want("10110000")
	if n := g.r.Block(g.now, core.OutcomeSkippedSensor); n != 1 {
		t.Errorf("blocked %d runs, want 1", n)
	}
	g.want("10010000") // C stopped; D ignores the sensor; manual A keeps running
	if r := g.st.runs[ids[0]]; r.Outcome != core.OutcomeSkippedSensor {
		t.Errorf("%+v", r)
	}
	_ = fmt.Sprint
}

func TestShutdownAllOff(t *testing.T) {
	g := newRig(t, nil)
	g.enqueue(Request{ZoneID: 3, Seconds: 60})
	g.advance(2)
	if err := g.r.Shutdown(g.now); err != nil {
		t.Fatal(err)
	}
	g.want("00000000")
}

func TestClockBackwards(t *testing.T) {
	g := newRig(t, nil)
	id := g.enqueue(Request{ZoneID: 3, Seconds: 60})[0]
	g.advance(10)
	g.now = g.now.Add(-time.Hour)
	g.advance(50)
	g.want("00000000")
	if r := g.st.runs[id]; r.Outcome != core.OutcomeDone || r.ActualSeconds < 59 || r.ActualSeconds > 61 {
		t.Errorf("%+v", r)
	}
}

func TestSnapshotEstimates(t *testing.T) {
	g := newRig(t, nil)
	g.enqueue(Request{ZoneID: 1, Seconds: 60}, Request{ZoneID: 2, Seconds: 30})
	g.advance(1)
	st := g.r.Snapshot(g.now)
	if len(st.Running) != 1 || len(st.Queue) != 1 {
		t.Fatalf("%+v", st)
	}
	want := t0.Add(70 * time.Second) // A opened at t0 for 60 s, then 10 s delay
	if !st.Queue[0].EstStart.Equal(want) {
		t.Errorf("queued est start %v want %v", st.Queue[0].EstStart, want)
	}
}

func TestManualRunsInParallelUpToMaxOpen(t *testing.T) {
	g := newRig(t, nil) // A and B share group 1; max 2 open
	ids := g.enqueue(Request{ZoneID: 1, Seconds: 60, Manual: true})
	g.enqueue(Request{ZoneID: 2, Seconds: 30, Manual: true})
	third := g.enqueue(Request{ZoneID: 3, Seconds: 30, Manual: true})
	g.want("11000000") // same group, but manual: both open; C waits for a slot
	st := g.r.Snapshot(g.now)
	if len(st.Running) != 2 || len(st.Queue) != 1 || st.Queue[0].RunID != third[0] {
		t.Fatalf("snapshot: %+v", st)
	}
	g.advance(30)
	g.want("10100000") // B done, C takes the free slot
	g.advance(30)
	g.want("00000000")
	if r := g.st.runs[ids[0]]; r.Outcome != core.OutcomeDone || r.ActualSeconds != 60 {
		t.Errorf("%+v", r)
	}
	// Stopping one of two open runs starts the waiting one immediately.
	a := g.enqueue(Request{ZoneID: 1, Seconds: 60, Manual: true})
	g.enqueue(Request{ZoneID: 2, Seconds: 60, Manual: true}, Request{ZoneID: 4, Seconds: 60, Manual: true})
	g.want("11000000")
	g.r.StopRun(g.now, a[0])
	g.want("01010000")
}

func TestManualPausesWholeGroup(t *testing.T) {
	g := newRig(t, func(s *core.Settings, z []core.Zone) { s.MaxOpenZones = 4 })
	sched := g.enqueue(Request{ZoneID: 1, Seconds: 100, ProgramID: pid(1)}, Request{ZoneID: 2, Seconds: 50, ProgramID: pid(1)})
	g.enqueue(Request{ZoneID: 3, Seconds: 100, ProgramID: pid(2)}) // independent zone
	g.advance(10)
	g.want("10100000")
	g.enqueue(Request{ZoneID: 2, Seconds: 20, Manual: true}) // manual B, same group as scheduled A
	g.want("01100000")                                       // A paused; C (other lane) keeps running
	g.advance(20)
	g.want("00100000") // manual done: station delay before the group resumes
	g.advance(10)
	g.want("10100000") // A resumes with its remaining 90 s
	g.advance(90)
	if r := g.st.runs[sched[0]]; r.Outcome != core.OutcomeDone || r.ActualSeconds != 100 {
		t.Errorf("scheduled A: %+v", r)
	}
	if _, err := g.r.Enqueue(g.now, []Request{{ZoneID: 4, Seconds: 30, Manual: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.r.Enqueue(g.now, []Request{{ZoneID: 4, Seconds: 30, Manual: true}}); !errors.Is(err, ErrAlreadyQueued) {
		t.Errorf("second manual run of the same zone: %v", err)
	}
}
