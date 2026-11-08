// Package runner owns the valve queue and is the only code that calls the
// output driver. Every exit path ends in AllOff().
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sprinkler/internal/core"
	"sprinkler/internal/hw"
)

// Store is the persistence the runner needs.
type Store interface {
	InsertRun(*core.Run) error
	UpdateRun(*core.Run) error
	AddEvent(t time.Time, kind string, detail any) error
}

// Request asks for one zone run.
type Request struct {
	ZoneID         int64
	Seconds        int
	ProgramID      *int64
	PlannedStart   time.Time
	PlannedSeconds int
	ScalePct       int
	Manual         bool
}

type itemState int

const (
	queued  itemState = iota
	waiting           // master valve opened, waiting for its on-delay
	running
)

type item struct {
	run      core.Run
	zone     core.Zone
	manual   bool
	target   int // seconds to water in total
	state    itemState
	openedAt time.Time // start of the current open segment
	waitTill time.Time
}

func (it *item) used(now time.Time) int {
	n := it.run.ActualSeconds
	if it.state == running {
		n += int(now.Sub(it.openedAt) / time.Second)
	}
	return n
}

type lane struct {
	items  []*item
	freeAt time.Time
}

// Runner executes zone runs one second at a time.
type Runner struct {
	mu       sync.Mutex
	out      hw.Outputs
	store    Store
	log      *slog.Logger
	loc      *time.Location
	zones    map[int64]core.Zone
	settings core.Settings

	lanes       map[string]*lane
	paused      bool
	masterOn    bool
	masterOffAt time.Time
	lastStates  []bool
	fault       string
	day         string
	dayUsed     map[int64]int // seconds watered today per zone, closed segments
	lastBeat    time.Time
	lastNow     time.Time

	// OnChange is called (without the lock) after state changes.
	OnChange func()
	// Alive is called after every successful tick (systemd watchdog).
	Alive func()
}

// New creates a runner and switches every output off.
func New(out hw.Outputs, st Store, loc *time.Location, log *slog.Logger) (*Runner, error) {
	if log == nil {
		log = slog.Default()
	}
	r := &Runner{out: out, store: st, loc: loc, log: log, lanes: map[string]*lane{}, zones: map[int64]core.Zone{}, dayUsed: map[int64]int{}}
	if err := out.AllOff(); err != nil {
		return nil, fmt.Errorf("initial all-off: %w", err)
	}
	r.lastStates = make([]bool, out.Count())
	return r, nil
}

// Configure updates zones and settings (safe while running).
func (r *Runner) Configure(zones []core.Zone, s core.Settings) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.zones = map[int64]core.Zone{}
	for _, z := range zones {
		r.zones[z.ID] = z
	}
	// Queued and running items follow zone edits at once (a lower
	// max_minutes or a moved output applies on the next tick).
	for _, l := range r.lanes {
		for _, it := range l.items {
			if z, ok := r.zones[it.zone.ID]; ok {
				it.zone = z
			}
		}
	}
	r.settings = s
	if loc, err := s.Location(); err == nil {
		r.loc = loc
	}
}

// SetDayUsage seeds today's watered seconds per zone (from the store at
// start-up) for the daily cap.
func (r *Runner) SetDayUsage(day string, used map[int64]int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.day = day
	r.dayUsed = map[int64]int{}
	for k, v := range used {
		r.dayUsed[k] = v
	}
}

var (
	ErrUnknownZone   = errors.New("unknown zone")
	ErrZoneDisabled  = errors.New("zone is disabled")
	ErrAlreadyQueued = errors.New("zone already has a manual run")
	ErrFault         = errors.New("outputs are in a fault state; clear it first")
	ErrBadDuration   = errors.New("duration must be 1 s – 12 h")
	ErrMasterZone    = errors.New("the master valve cannot be run as a zone")
)

// Enqueue adds runs. Scheduled runs go to the back of their group's lane
// and run one after another. Manual runs get a lane of their own per zone,
// so several can run side by side up to MaxOpenZones; while one is queued
// or running, scheduled runs of the same zone or group are paused. It
// returns the run ids.
func (r *Runner) Enqueue(now time.Time, reqs []Request) ([]int64, error) {
	var ids []int64
	var err error
	r.locked(func() {
		ids, err = r.enqueueLocked(now, reqs)
		if len(ids) > 0 {
			r.tickLocked(now) // open valves now, not on the next tick
		}
	})
	r.changed()
	return ids, err
}

func (r *Runner) enqueueLocked(now time.Time, reqs []Request) ([]int64, error) {
	if r.fault != "" {
		return nil, ErrFault
	}
	// Validate everything first so a batch is all-or-nothing.
	for _, q := range reqs {
		z, ok := r.zones[q.ZoneID]
		if !ok {
			return nil, ErrUnknownZone
		}
		if q.ZoneID == r.settings.MasterZoneID {
			return nil, ErrMasterZone
		}
		if !z.Enabled && !q.Manual {
			return nil, ErrZoneDisabled
		}
		if q.Seconds < 1 || q.Seconds > 12*3600 {
			return nil, ErrBadDuration
		}
		if q.Manual {
			if l := r.lanes[manualLane(z)]; l != nil && len(l.items) > 0 {
				return nil, ErrAlreadyQueued
			}
		}
	}
	var ids []int64
	for _, q := range reqs {
		z := r.zones[q.ZoneID]
		ps := q.PlannedStart
		if ps.IsZero() {
			ps = now
		}
		planned := q.PlannedSeconds
		if planned == 0 {
			planned = q.Seconds
		}
		scale := q.ScalePct
		if scale == 0 {
			scale = 100
		}
		it := &item{
			run:    core.Run{ZoneID: z.ID, ProgramID: q.ProgramID, PlannedStart: ps.UTC(), PlannedSeconds: planned, ScalePct: scale},
			zone:   z,
			manual: q.Manual,
			target: q.Seconds,
		}
		if err := r.store.InsertRun(&it.run); err != nil {
			return ids, fmt.Errorf("store run: %w", err)
		}
		ids = append(ids, it.run.ID)
		if q.Manual {
			// Pause scheduled runs this manual run conflicts with.
			for _, sl := range r.lanes {
				for _, other := range sl.items {
					if !other.manual && other.state != queued && conflicts(z, other.zone) {
						r.suspend(other, now)
					}
				}
			}
			l := r.lane(manualLane(z))
			l.items = append(l.items, it)
		} else {
			l := r.laneFor(z)
			l.items = append(l.items, it)
		}
	}
	return ids, nil
}

func (r *Runner) laneFor(z core.Zone) *lane { return r.lane(z.Lane()) }

func (r *Runner) lane(k string) *lane {
	l := r.lanes[k]
	if l == nil {
		l = &lane{}
		r.lanes[k] = l
	}
	return l
}

// manualLane is the lane key for manual runs of a zone: one per zone, so
// manual runs don't queue behind each other.
func manualLane(z core.Zone) string { return "m" + strconv.FormatInt(z.ID, 10) }

func isManualLane(k string) bool { return strings.HasPrefix(k, "m") }

// conflicts reports whether a manual run of a pauses scheduled runs of b:
// the same zone, or the same sequential group.
func conflicts(a, b core.Zone) bool {
	return a.ID == b.ID || (a.GroupID > 0 && a.GroupID == b.GroupID)
}

// blockedByManual reports whether a scheduled run must wait because a
// manual run of the same zone or group is queued or running.
func (r *Runner) blockedByManual(it *item) bool {
	if it.manual {
		return false
	}
	for k, l := range r.lanes {
		if !isManualLane(k) {
			continue
		}
		for _, m := range l.items {
			if conflicts(m.zone, it.zone) {
				return true
			}
		}
	}
	return false
}

// suspend closes the current open segment of a running item and puts it
// back to queued (pause, or pre-empted by a manual run).
func (r *Runner) suspend(it *item, now time.Time) {
	if it.state == running {
		seg := int(now.Sub(it.openedAt) / time.Second)
		it.run.ActualSeconds += seg
		r.dayUsed[it.zone.ID] += seg
	}
	it.state = queued
	_ = r.store.UpdateRun(&it.run)
}

// ProgramActive reports whether any run of the program is queued or running.
func (r *Runner) ProgramActive(programID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.lanes {
		for _, it := range l.items {
			if it.run.ProgramID != nil && *it.run.ProgramID == programID {
				return true
			}
		}
	}
	return false
}

// LaneFree returns when each lane will be free given the current queue
// (estimate, for planning previews).
func (r *Runner) LaneFree(now time.Time) map[string]time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]time.Time{}
	for k, l := range r.lanes {
		t := now
		if l.freeAt.After(t) {
			t = l.freeAt
		}
		for _, it := range l.items {
			t = t.Add(time.Duration(it.target-it.used(now)) * time.Second)
			t = t.Add(time.Duration(r.settings.StationDelayS) * time.Second)
		}
		out[k] = t
	}
	return out
}

// finish closes a run with an outcome and removes it from its lane.
func (r *Runner) finish(l *lane, idx int, now time.Time, outcome string) {
	it := l.items[idx]
	wasOpen := it.state == running
	if wasOpen {
		seg := int(now.Sub(it.openedAt) / time.Second)
		it.run.ActualSeconds += seg
		r.dayUsed[it.zone.ID] += seg
	}
	if it.run.Start != nil || it.run.ActualSeconds > 0 {
		t := now.UTC()
		it.run.End = &t
	}
	it.run.Outcome = outcome
	if err := r.store.UpdateRun(&it.run); err != nil {
		r.log.Error("store run", "err", err)
	}
	l.items = append(l.items[:idx], l.items[idx+1:]...)
	if wasOpen {
		free := now.Add(time.Duration(r.settings.StationDelayS) * time.Second)
		if !it.manual {
			l.freeAt = free // manual lanes don't wait: the delay is for valves taking turns in a group
		} else {
			// The paused scheduled runs of this zone's group also wait the delay.
			if gl := r.lanes[it.zone.Lane()]; gl != nil && gl.freeAt.Before(free) {
				gl.freeAt = free
			}
		}
	}
	r.log.Info("run finished", "zone", it.zone.Name, "outcome", outcome, "seconds", it.run.ActualSeconds)
}

// StopAll clears every queue and closes all valves.
func (r *Runner) StopAll(now time.Time) {
	r.locked(func() {
		for _, l := range r.lanes {
			for len(l.items) > 0 {
				r.finish(l, 0, now, core.OutcomeStopped)
			}
			l.freeAt = time.Time{}
		}
		r.masterOn = false // stop all closes the master at once, no off-delay
		r.masterOffAt = time.Time{}
		r.applyLocked(now)
	})
	_ = r.store.AddEvent(now, "stop_all", nil)
	r.changed()
}

// StopZone stops and dequeues every run of one zone.
func (r *Runner) StopZone(now time.Time, zoneID int64) {
	r.locked(func() {
		for _, l := range r.lanes {
			for i := 0; i < len(l.items); {
				if l.items[i].zone.ID == zoneID {
					r.finish(l, i, now, core.OutcomeStopped)
					continue
				}
				i++
			}
		}
		r.tickLocked(now)
	})
	r.changed()
}

// StopRun stops or dequeues one run by id.
func (r *Runner) StopRun(now time.Time, runID int64) bool {
	found := false
	r.locked(func() {
		for _, l := range r.lanes {
			for i, it := range l.items {
				if it.run.ID == runID {
					r.finish(l, i, now, core.OutcomeStopped)
					found = true
					break
				}
			}
		}
		r.tickLocked(now) // a freed slot goes to the next waiting run at once
	})
	r.changed()
	return found
}

// Block stops scheduled (not manual) runs of zones that do not ignore the
// given condition, e.g. when the rain sensor trips mid-cycle.
func (r *Runner) Block(now time.Time, outcome string) int {
	n := 0
	r.locked(func() {
		for _, l := range r.lanes {
			for i := 0; i < len(l.items); {
				it := l.items[i]
				z := r.zones[it.zone.ID]
				ignore := (outcome == core.OutcomeSkippedRain && z.IgnoreRain) || (outcome == core.OutcomeSkippedSensor && z.IgnoreSensor)
				if !it.manual && !ignore {
					r.finish(l, i, now, outcome)
					n++
					continue
				}
				i++
			}
		}
		r.applyLocked(now)
	})
	if n > 0 {
		r.changed()
	}
	return n
}

// Pause closes all valves and freezes the queue; Resume continues it.
func (r *Runner) Pause(now time.Time) {
	r.locked(func() {
		if r.paused {
			return
		}
		r.paused = true
		for _, l := range r.lanes {
			for _, it := range l.items {
				if it.state != queued {
					r.suspend(it, now)
				}
			}
		}
		r.applyLocked(now)
	})
	_ = r.store.AddEvent(now, "pause", nil)
	r.changed()
}

// Resume continues a paused queue.
func (r *Runner) Resume(now time.Time) {
	r.locked(func() {
		r.paused = false
		r.tickLocked(now)
	})
	_ = r.store.AddEvent(now, "resume", nil)
	r.changed()
}

// ClearFault acknowledges an output failure so runs can be queued again.
func (r *Runner) ClearFault(now time.Time) {
	r.locked(func() { r.fault = "" })
	_ = r.store.AddEvent(now, "fault_cleared", nil)
	r.changed()
}

// Tick advances the state machine to now and writes the outputs.
func (r *Runner) Tick(now time.Time) {
	var changed bool
	r.locked(func() { changed = r.tickLocked(now) })
	if changed {
		r.changed()
	}
	if r.Alive != nil {
		r.Alive()
	}
}

// maxStep is how far the wall clock may move apart from real time between
// two ticks before it counts as a clock step.
const maxStep = 5 * time.Second

// hasMonotonic reports whether t carries a monotonic clock reading (times
// from time.Now do; fake clocks and parsed times don't).
func hasMonotonic(t time.Time) bool { return t != t.Round(0) }

func (r *Runner) tickLocked(now time.Time) bool {
	before := r.signature()
	r.clockStep(now)
	r.lastNow = now
	if d := now.In(r.loc).Format("2006-01-02"); d != r.day {
		r.day = d
		r.dayUsed = map[int64]int{}
	}
	return r.advanceLocked(now, before)
}

// clockStep handles the wall clock moving apart from real time since the
// last tick (NTP step, manual change).
//
// Times from time.Now carry a monotonic reading, and Sub between two of
// them measures real time, so open segments, delays and caps stay right
// across a step, which is only logged. A late tick (slow SD card) is not
// a step: the valve really was open that long.
//
// Without monotonic readings (fake clocks in tests) a step can't be told
// from a late tick, so any gap over maxStep, or backwards, counts as one:
// open segments close at the last known time and continue from now, so a
// step neither ends a run early nor counts water that was not given.
func (r *Runner) clockStep(now time.Time) {
	if r.lastNow.IsZero() {
		return
	}
	wall := now.Round(0).Sub(r.lastNow.Round(0))
	if hasMonotonic(now) && hasMonotonic(r.lastNow) {
		if d := wall - now.Sub(r.lastNow); d > maxStep || d < -maxStep {
			r.log.Warn("clock step", "by", d)
			_ = r.store.AddEvent(now, "clock_jump", map[string]any{"from": r.lastNow.UTC(), "to": now.UTC()})
		}
		return
	}
	if wall >= 0 && wall <= maxStep {
		return
	}
	for _, l := range r.lanes {
		for _, it := range l.items {
			if it.state == running {
				seg := int(r.lastNow.Sub(it.openedAt)/time.Second) + 1
				if seg < 0 {
					seg = 0
				}
				it.run.ActualSeconds += seg
				r.dayUsed[it.zone.ID] += seg
				it.openedAt = now
			}
			if it.state == waiting {
				it.waitTill = now
			}
		}
		if !l.freeAt.IsZero() {
			l.freeAt = now
		}
	}
	_ = r.store.AddEvent(now, "clock_jump", map[string]any{"from": r.lastNow.UTC(), "to": now.UTC()})
}

// advanceLocked finishes and starts runs at now and writes the outputs.
// before is the state signature at the start of the tick.
func (r *Runner) advanceLocked(now time.Time, before string) bool {
	capS := r.settings.DailyCapMinutes * 60
	keys := r.laneKeys()

	// 1. Finish runs that are done or over a cap.
	for _, k := range keys {
		l := r.lanes[k]
		for i := 0; i < len(l.items); {
			it := l.items[i]
			if it.state != running {
				i++
				continue
			}
			used := it.used(now)
			seg := int(now.Sub(it.openedAt) / time.Second)
			maxS := it.zone.MaxMinutes * 60
			switch {
			case used >= it.target:
				r.finish(l, i, now, core.OutcomeDone)
			case maxS > 0 && used >= maxS:
				_ = r.store.AddEvent(now, "cut_max", map[string]any{"zone_id": it.zone.ID, "max_minutes": it.zone.MaxMinutes})
				r.finish(l, i, now, core.OutcomeCutMax)
			case capS > 0 && r.dayUsed[it.zone.ID]+seg >= capS:
				_ = r.store.AddEvent(now, "cut_daily_cap", map[string]any{"zone_id": it.zone.ID, "cap_minutes": r.settings.DailyCapMinutes})
				r.finish(l, i, now, core.OutcomeCutMax)
			default:
				i++
			}
		}
	}

	// 2. Start lane heads.
	if !r.paused && r.fault == "" {
		open := 0
		for _, l := range r.lanes {
			for _, it := range l.items {
				if it.state != queued {
					open++
				}
			}
		}
		for _, k := range keys {
			l := r.lanes[k]
			if len(l.items) == 0 {
				continue
			}
			it := l.items[0]
			if it.state == queued {
				if now.Before(l.freeAt) || r.blockedByManual(it) {
					continue
				}
				if r.settings.MaxOpenZones > 0 && open >= r.settings.MaxOpenZones {
					continue
				}
				if capS > 0 && r.dayUsed[it.zone.ID] >= capS {
					_ = r.store.AddEvent(now, "cut_daily_cap", map[string]any{"zone_id": it.zone.ID, "cap_minutes": r.settings.DailyCapMinutes})
					r.finish(l, 0, now, core.OutcomeCutMax)
					continue
				}
				open++
				if r.usesMaster(it) && !r.masterOn && r.settings.MasterOnDelayS > 0 {
					it.state = waiting
					it.waitTill = now.Add(time.Duration(r.settings.MasterOnDelayS) * time.Second)
				} else {
					r.open(it, now)
				}
			} else if it.state == waiting && !now.Before(it.waitTill) {
				r.open(it, now)
			}
		}
	}

	// 3. Heartbeat: persist progress every 30 s for power-loss recovery.
	if now.Sub(r.lastBeat) >= 30*time.Second {
		r.lastBeat = now
		for _, l := range r.lanes {
			for _, it := range l.items {
				if it.state == running {
					cp := it.run
					cp.ActualSeconds = it.used(now)
					_ = r.store.UpdateRun(&cp)
				}
			}
		}
	}

	r.applyLocked(now)
	return r.signature() != before
}

func (r *Runner) open(it *item, now time.Time) {
	it.state = running
	it.openedAt = now
	if it.run.Start == nil {
		t := now.UTC()
		it.run.Start = &t
	}
	if err := r.store.UpdateRun(&it.run); err != nil {
		r.log.Error("store run", "err", err)
	}
	r.log.Info("run started", "zone", it.zone.Name, "seconds", it.target-it.run.ActualSeconds)
}

func (r *Runner) usesMaster(it *item) bool {
	return r.settings.MasterZoneID > 0 && it.zone.UsesMaster
}

// laneKeys returns lanes in start priority: manual lanes first (they get
// free slots before scheduled runs), then by name.
func (r *Runner) laneKeys() []string {
	keys := make([]string, 0, len(r.lanes))
	for k := range r.lanes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		mi, mj := isManualLane(keys[i]), isManualLane(keys[j])
		if mi != mj {
			return mi
		}
		return keys[i] < keys[j]
	})
	return keys
}

// applyLocked computes the desired outputs and writes them if they changed.
func (r *Runner) applyLocked(now time.Time) {
	states := make([]bool, r.out.Count())
	masterNeeded := false
	for _, l := range r.lanes {
		for _, it := range l.items {
			if it.state == running {
				if it.zone.OutputIndex >= 0 && it.zone.OutputIndex < len(states) {
					states[it.zone.OutputIndex] = true
				}
			}
			if it.state != queued && r.usesMaster(it) {
				masterNeeded = true
			}
		}
	}
	if r.settings.MasterZoneID > 0 {
		switch {
		case masterNeeded:
			r.masterOn = true
			r.masterOffAt = time.Time{}
		case r.masterOn && r.masterOffAt.IsZero() && r.settings.MasterOffDelayS > 0 && !r.paused:
			r.masterOffAt = now.Add(time.Duration(r.settings.MasterOffDelayS) * time.Second)
		case r.masterOn && (r.masterOffAt.IsZero() || !now.Before(r.masterOffAt) || r.paused):
			r.masterOn = false
			r.masterOffAt = time.Time{}
		}
		if r.masterOn {
			if mz, ok := r.zones[r.settings.MasterZoneID]; ok && mz.OutputIndex < len(states) {
				states[mz.OutputIndex] = true
			}
		}
	} else {
		r.masterOn = false
	}
	same := len(states) == len(r.lastStates)
	for i := range states {
		same = same && states[i] == r.lastStates[i]
	}
	if same {
		return
	}
	err := r.out.Set(states)
	if err != nil {
		r.log.Warn("output write failed, retrying", "err", err)
		err = r.out.Set(states)
	}
	if err != nil {
		r.faultLocked(now, err)
		return
	}
	r.lastStates = states
}

func (r *Runner) faultLocked(now time.Time, cause error) {
	r.log.Error("output write failed twice; all off", "err", cause)
	offErr := r.out.AllOff()
	r.fault = fmt.Sprintf("Valve output failed: %v", cause)
	if offErr != nil {
		r.fault += fmt.Sprintf(" (all-off also failed: %v)", offErr)
	}
	for _, l := range r.lanes {
		for len(l.items) > 0 {
			r.finish(l, 0, now, core.OutcomeError)
		}
	}
	r.masterOn = false
	r.lastStates = make([]bool, r.out.Count())
	_ = r.store.AddEvent(now, "output_error", map[string]any{"error": cause.Error()})
}

// signature summarises what the UI shows, to tell whether a tick changed
// anything. It runs twice a second, so it avoids fmt.
func (r *Runner) signature() string {
	b := make([]byte, 0, 64)
	b = strconv.AppendBool(b, r.paused)
	b = strconv.AppendBool(b, r.masterOn)
	b = append(b, r.fault...)
	b = append(b, 0)
	for _, k := range r.laneKeys() {
		for _, it := range r.lanes[k].items {
			b = strconv.AppendInt(b, it.run.ID, 10)
			b = append(b, ':')
			b = strconv.AppendInt(b, int64(it.state), 10)
			b = append(b, ',')
		}
	}
	return string(b)
}

func (r *Runner) changed() {
	if r.OnChange != nil {
		r.OnChange()
	}
}

// locked runs f under the lock. The deferred unlock matters: a panic in f
// that a caller recovers (net/http does, per request) must not leave the
// runner locked, or the tick loop would stall with valves open until the
// systemd watchdog fires.
func (r *Runner) locked(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f()
}

// Shutdown stops all runs and switches everything off. Call it on exit.
func (r *Runner) Shutdown(now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.lanes {
		for len(l.items) > 0 {
			r.finish(l, 0, now, core.OutcomeStopped)
		}
	}
	r.masterOn = false
	return r.out.AllOff()
}

// Run ticks once a second until ctx is done, then shuts down. A panic
// inside a tick switches all outputs off before propagating.
func (r *Runner) Run(ctx context.Context, clock func() time.Time) error {
	defer func() {
		if p := recover(); p != nil {
			_ = r.out.AllOff()
			panic(p)
		}
	}()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return r.Shutdown(clock())
		case <-t.C:
			r.Tick(clock())
		}
	}
}

// RunView is a queued or running run for the UI.
type RunView struct {
	RunID     int64     `json:"run_id"`
	ZoneID    int64     `json:"zone_id"`
	ProgramID *int64    `json:"program_id"`
	Manual    bool      `json:"manual"`
	State     string    `json:"state"` // queued | waiting | running
	Seconds   int       `json:"seconds"`
	Remaining int       `json:"remaining"`
	EstStart  time.Time `json:"est_start"`
	EstEnd    time.Time `json:"est_end"`
}

// State is a snapshot for the UI.
type State struct {
	Paused   bool      `json:"paused"`
	Fault    string    `json:"fault"`
	MasterOn bool      `json:"master_on"`
	Outputs  []bool    `json:"outputs"`
	Driver   string    `json:"driver"`
	Running  []RunView `json:"running"`
	Queue    []RunView `json:"queue"`
}

// Snapshot returns the current state with estimated times.
func (r *Runner) Snapshot(now time.Time) State {
	now = now.UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	st := State{
		Paused: r.paused, Fault: r.fault, MasterOn: r.masterOn, Outputs: append([]bool(nil), r.lastStates...), Driver: r.out.Name(),
		Running: []RunView{}, Queue: []RunView{},
	}
	delay := time.Duration(r.settings.StationDelayS) * time.Second
	for _, k := range r.laneKeys() {
		l := r.lanes[k]
		cursor := now
		if l.freeAt.After(cursor) {
			cursor = l.freeAt
		}
		for _, it := range l.items {
			rem := it.target - it.used(now)
			if rem < 0 {
				rem = 0
			}
			v := RunView{RunID: it.run.ID, ZoneID: it.zone.ID, ProgramID: it.run.ProgramID, Manual: it.manual, Seconds: it.target, Remaining: rem}
			switch it.state {
			case running:
				v.State = "running"
				v.EstStart = *it.run.Start
				v.EstEnd = now.Add(time.Duration(rem) * time.Second)
				cursor = v.EstEnd.Add(delay)
				st.Running = append(st.Running, v)
			case waiting:
				v.State = "waiting"
				v.EstStart = it.waitTill
				v.EstEnd = it.waitTill.Add(time.Duration(rem) * time.Second)
				cursor = v.EstEnd.Add(delay)
				st.Running = append(st.Running, v)
			default:
				v.State = "queued"
				v.EstStart = cursor
				v.EstEnd = cursor.Add(time.Duration(rem) * time.Second)
				cursor = v.EstEnd.Add(delay)
				st.Queue = append(st.Queue, v)
			}
		}
	}
	sort.SliceStable(st.Queue, func(i, j int) bool { return st.Queue[i].EstStart.Before(st.Queue[j].EstStart) })
	return st
}
