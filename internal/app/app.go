// Package app wires the planner, the runner, the store and the sensor
// together: once a minute it turns program starts into queued runs, and
// once a second it follows the rain/soil sensor.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"sprinkler/internal/core"
	"sprinkler/internal/hw"
	"sprinkler/internal/runner"
	"sprinkler/internal/schedule"
	"sprinkler/internal/store"
)

// App is the running controller.
type App struct {
	Store     *store.Store
	Runner    *runner.Runner
	Sensor    hw.Input     // nil when no sensor is configured in hardware
	SimSensor *hw.SimInput // set when the sensor is simulated
	Clock     func() time.Time
	Log       *slog.Logger
	Started   time.Time
	Version   string
	// ClockSynced reports whether the system clock is set from NTP; nil
	// means always. Scheduled starts wait for the first sync.
	ClockSynced func() bool

	// SnapshotDir receives a checked copy of the database once a day
	// ("" = off); SnapshotKeep is how many days are kept.
	SnapshotDir  string
	SnapshotKeep int

	clockOK      atomic.Bool
	clockWaiting atomic.Bool

	snapMu  sync.Mutex
	snapDay string    // local day of the last good snapshot
	snapTry time.Time // last attempt (failures retry hourly)
	snapErr string

	mu       sync.RWMutex
	settings core.Settings
	zones    []core.Zone
	programs []core.Program
	loc      *time.Location

	lastPlan  time.Time
	lastPrune time.Time
	sensorRaw bool
	rawSince  time.Time
	sensorWet bool
	sensorErr string
	rainWasOn bool

	subMu sync.Mutex
	subs  map[chan struct{}]struct{}
}

// New builds the app and loads configuration from the store.
func New(st *store.Store, r *runner.Runner, log *slog.Logger) (*App, error) {
	a := &App{Store: st, Runner: r, Clock: time.Now, Log: log, subs: map[chan struct{}]struct{}{}}
	a.Started = a.Clock()
	r.OnChange = a.Notify
	if err := a.Reload(); err != nil {
		return nil, err
	}
	return a, nil
}

// Reload re-reads zones, programs and settings and reconfigures the runner.
func (a *App) Reload() error {
	s, err := a.Store.Settings()
	if err != nil {
		return err
	}
	loc, err := s.Location()
	if err != nil {
		return fmt.Errorf("time zone %q: %w", s.Timezone, err)
	}
	zs, err := a.Store.Zones()
	if err != nil {
		return err
	}
	ps, err := a.Store.Programs()
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.settings, a.zones, a.programs, a.loc = s, zs, ps, loc
	a.mu.Unlock()
	a.Runner.Configure(zs, s)
	a.Notify()
	return nil
}

// Snapshot returns the cached configuration.
func (a *App) Snapshot() (core.Settings, []core.Zone, []core.Program, *time.Location) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.settings, a.zones, a.programs, a.loc
}

func zoneMap(zs []core.Zone) map[int64]core.Zone {
	m := make(map[int64]core.Zone, len(zs))
	for _, z := range zs {
		m[z.ID] = z
	}
	return m
}

// Recover closes runs left open by a crash or power loss. Their duration
// is what the last heartbeat recorded.
func (a *App) Recover(now time.Time) error {
	open, err := a.Store.OpenRuns()
	if err != nil {
		return err
	}
	for i := range open {
		r := &open[i]
		r.Outcome = core.OutcomeError
		if r.Start != nil {
			end := r.Start.Add(time.Duration(r.ActualSeconds) * time.Second)
			r.End = &end
		}
		if err := a.Store.UpdateRun(r); err != nil {
			return err
		}
	}
	if len(open) > 0 {
		a.Log.Warn("closed runs left open by an unclean stop", "count", len(open))
		_ = a.Store.AddEvent(now, "crash_recovery", map[string]any{"open_runs": len(open)})
	}
	_ = a.Store.AddEvent(now, "boot", map[string]any{"version": a.Version})
	// Seed the per-zone daily cap with what was already watered today.
	a.mu.RLock()
	loc := a.loc
	a.mu.RUnlock()
	ln := now.In(loc)
	midnight := time.Date(ln.Year(), ln.Month(), ln.Day(), 0, 0, 0, 0, loc)
	used, err := a.Store.ZoneSecondsSince(midnight)
	if err != nil {
		return err
	}
	a.Runner.SetDayUsage(ln.Format("2006-01-02"), used)
	return nil
}

// Conditions returns blocking/scaling facts at time t.
func (a *App) Conditions(t time.Time) schedule.Conditions {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return schedule.Conditions{
		WateringLevelPct: a.settings.WateringLevelPct,
		RainDelay:        a.settings.RainDelayUntil != nil && t.Before(*a.settings.RainDelayUntil),
		SensorWet:        a.settings.Sensor.Enabled && a.sensorWet,
	}
}

// clockReady reports whether scheduled starts may be planned. Until the
// clock has synced once, the time may be fake-hwclock's stale guess, so
// programs wait (and their missed starts are skipped, not run late).
// After the first sync the clock is trusted even if NTP drops out later.
func (a *App) clockReady(now time.Time) bool {
	if a.clockOK.Load() {
		return true
	}
	if a.ClockSynced == nil || a.ClockSynced() {
		a.clockOK.Store(true)
		if a.clockWaiting.Swap(false) {
			a.Log.Info("clock synced, scheduling programs")
			_ = a.Store.AddEvent(now, "clock_synced", nil)
			a.Notify()
		}
		return true
	}
	if !a.clockWaiting.Swap(true) {
		a.Log.Warn("clock not synced yet, programs wait")
		_ = a.Store.AddEvent(now, "clock_unsynced", nil)
		a.Notify()
	}
	return false
}

// ClockTrusted reports whether the clock has synced since start-up.
func (a *App) ClockTrusted() bool { return a.clockOK.Load() }

// ClockWaiting reports whether scheduled programs are held until the
// clock syncs.
func (a *App) ClockWaiting() bool { return a.clockWaiting.Load() }

// PlanTick queues program starts that fell due since the last call.
func (a *App) PlanTick(now time.Time) {
	if !a.clockReady(now) {
		return // lastPlan stays zero: planning starts fresh after sync
	}
	settings, zones, programs, loc := a.Snapshot()
	// Plan on the wall clock. Times from time.Now carry a monotonic
	// reading, and comparing two of them ignores the wall clock, which
	// would hide an NTP or manual step; the planned starts are wall times.
	// The runner still gets now as is, to time valves on the monotonic
	// clock.
	wall := now.Round(0)
	from := a.lastPlan
	switch {
	case from.IsZero():
		// Start-up: only starts in the current minute (a restart inside
		// the start minute must still water; earlier ones are missed).
		from = wall.Truncate(time.Minute).Add(-time.Nanosecond)
	case wall.Sub(from) > 5*time.Minute || from.Sub(wall) > 5*time.Second:
		a.Log.Warn("clock jump, re-planning from now", "from", from, "to", wall)
		_ = a.Store.AddEvent(now, "clock_jump", map[string]any{"from": from.UTC(), "to": wall.UTC()})
		from = wall.Truncate(time.Minute).Add(-time.Nanosecond)
	case !wall.After(from):
		return
	}
	a.lastPlan = wall
	env := schedule.Env{Loc: loc, Lat: settings.Lat, Lon: settings.Lon}
	due := schedule.Due(programs, env, from, wall)
	if len(due) == 0 {
		return
	}
	zm := zoneMap(zones)
	byID := map[int64]core.Program{}
	for _, p := range programs {
		byID[p.ID] = p
	}
	skips, err := a.Store.Skips(from)
	if err != nil {
		a.Log.Error("load skips", "err", err)
	}
	for _, s := range due {
		p := byID[s.ProgramID]
		if done, err := a.Store.ProgramStarted(p.ID, s.Time); err != nil || done {
			continue // never start the same planned start twice
		}
		if skips[store.SkipKey{ProgramID: p.ID, Start: s.Time.Unix()}] {
			a.skipStart(now, p, s.Time, zm)
			continue
		}
		if !settings.ControllerOn {
			_ = a.Store.AddEvent(now, "program_skipped", map[string]any{"program_id": p.ID, "reason": "controller_off", "planned": s.Time.UTC()})
			continue
		}
		if a.Runner.ProgramActive(p.ID) {
			a.Log.Info("program still queued, start dropped", "program", p.Name)
			_ = a.Store.AddEvent(now, "program_overlap", map[string]any{"program_id": p.ID, "planned": s.Time.UTC()})
			continue
		}
		a.startProgram(now, p, s.Time, zm, a.Conditions(now))
	}
}

// skipStart records a start the user skipped: its zone runs go to the
// history as skipped_user, which also marks the start as handled.
func (a *App) skipStart(now time.Time, p core.Program, at time.Time, zm map[int64]core.Zone) {
	pid := p.ID
	for _, pr := range schedule.Expand(p, at, zm, a.Conditions(now)) {
		run := core.Run{ZoneID: pr.ZoneID, ProgramID: &pid, PlannedStart: at.UTC(), PlannedSeconds: pr.PlannedSeconds, ScalePct: pr.ScalePct, Outcome: core.OutcomeSkippedUser}
		_ = a.Store.InsertRun(&run)
	}
	a.Log.Info("program start skipped by user", "program", p.Name)
	_ = a.Store.AddEvent(now, "program_skipped", map[string]any{"program_id": p.ID, "reason": "user", "planned": at.UTC()})
	a.Notify()
}

// ErrNotPlanned means a skip names no future start of that program.
var ErrNotPlanned = errors.New("no such planned start")

// SetSkip skips (or un-skips) one future start of a program, as listed by
// Upcoming. The start must be one the planner would make.
func (a *App) SetSkip(programID int64, at time.Time, skip bool) error {
	settings, _, programs, loc := a.Snapshot()
	now := a.Clock()
	if !at.After(now) {
		return ErrNotPlanned
	}
	var prog *core.Program
	for i := range programs {
		if programs[i].ID == programID {
			prog = &programs[i]
		}
	}
	if prog == nil {
		return ErrNotPlanned
	}
	if skip {
		env := schedule.Env{Loc: loc, Lat: settings.Lat, Lon: settings.Lon}
		found := false
		for _, s := range schedule.Due([]core.Program{*prog}, env, at.Add(-time.Second), at.Add(time.Second)) {
			if s.Time.Equal(at) {
				found = true
			}
		}
		if !found {
			return ErrNotPlanned
		}
	}
	if err := a.Store.SetSkip(programID, at, skip); err != nil {
		return err
	}
	kind := "start_skip_set"
	if !skip {
		kind = "start_skip_cleared"
	}
	_ = a.Store.AddEvent(now, kind, map[string]any{"program_id": programID, "planned": at.UTC()})
	a.Notify()
	return nil
}

// startProgram expands a program start, logs skipped zones and queues the rest.
func (a *App) startProgram(now time.Time, p core.Program, at time.Time, zm map[int64]core.Zone, c schedule.Conditions) []int64 {
	planned := schedule.Expand(p, at, zm, c)
	var reqs []runner.Request
	pid := p.ID
	for _, pr := range planned {
		if pr.Skip != "" {
			run := core.Run{ZoneID: pr.ZoneID, ProgramID: &pid, PlannedStart: at.UTC(), PlannedSeconds: pr.PlannedSeconds, ScalePct: pr.ScalePct, Outcome: pr.Skip}
			_ = a.Store.InsertRun(&run)
			continue
		}
		reqs = append(reqs, runner.Request{ZoneID: pr.ZoneID, Seconds: pr.Seconds, ProgramID: &pid, PlannedStart: at, PlannedSeconds: pr.PlannedSeconds, ScalePct: pr.ScalePct})
	}
	a.Log.Info("program start", "program", p.Name, "zones", len(reqs), "skipped", len(planned)-len(reqs))
	if len(reqs) == 0 {
		a.Notify()
		return nil
	}
	ids, err := a.Runner.Enqueue(now, reqs)
	if err != nil {
		a.Log.Error("queue program", "program", p.Name, "err", err)
		_ = a.Store.AddEvent(now, "program_error", map[string]any{"program_id": p.ID, "error": err.Error()})
	}
	return ids
}

// RunProgramNow queues a program immediately (scaling applies, blocking
// does not: the user asked for it).
func (a *App) RunProgramNow(id int64) ([]int64, error) {
	_, zones, programs, _ := a.Snapshot()
	for _, p := range programs {
		if p.ID == id {
			if a.Runner.ProgramActive(id) {
				return nil, errors.New("program is already queued")
			}
			now := a.Clock()
			c := a.Conditions(now)
			c.RainDelay, c.SensorWet = false, false
			ids := a.startProgram(now, p, now, zoneMap(zones), c)
			if len(ids) == 0 {
				return nil, errors.New("nothing to run: no enabled zones or all runs too short")
			}
			return ids, nil
		}
	}
	return nil, errors.New("no such program")
}

// SensorTick reads the sensor and applies its on/off delays.
func (a *App) SensorTick(now time.Time) {
	settings, _, _, _ := a.Snapshot()
	in := a.Sensor
	if a.SimSensor != nil {
		in = a.SimSensor
	}
	if in == nil || !settings.Sensor.Enabled {
		a.mu.Lock()
		changed := a.sensorWet
		a.sensorWet = false
		a.mu.Unlock()
		if changed {
			a.Notify()
		}
		a.checkRainDelay(now)
		return
	}
	raw, err := in.Active()
	a.mu.Lock()
	if err != nil {
		if a.sensorErr == "" {
			a.Log.Error("sensor read", "err", err)
		}
		a.sensorErr = err.Error()
		a.mu.Unlock()
		return
	}
	a.sensorErr = ""
	if raw != a.sensorRaw || a.rawSince.IsZero() {
		a.sensorRaw = raw
		a.rawSince = now
	}
	delay := time.Duration(settings.Sensor.OffDelayS) * time.Second
	if raw {
		delay = time.Duration(settings.Sensor.OnDelayS) * time.Second
	}
	flip := raw != a.sensorWet && now.Sub(a.rawSince) >= delay
	if flip {
		a.sensorWet = raw
	}
	a.mu.Unlock()
	if flip {
		kind := "sensor_dry"
		if raw {
			kind = "sensor_wet"
		}
		a.Log.Info(kind)
		_ = a.Store.AddEvent(now, kind, map[string]any{"sensor": settings.Sensor.Kind})
		if raw {
			a.Runner.Block(now, core.OutcomeSkippedSensor)
		}
		a.Notify()
	}
	a.checkRainDelay(now)
}

func (a *App) checkRainDelay(now time.Time) {
	c := a.Conditions(now)
	if a.rainWasOn && !c.RainDelay {
		_ = a.Store.AddEvent(now, "rain_delay_ended", nil)
		a.Notify()
	}
	a.rainWasOn = c.RainDelay
}

// SetRainDelay starts (hours > 0) or clears (0) a rain delay.
func (a *App) SetRainDelay(hours float64) error {
	if hours < 0 || hours > 24*30 {
		return errors.New("rain delay must be 0–720 h")
	}
	now := a.Clock()
	s, err := a.Store.Settings()
	if err != nil {
		return err
	}
	if hours == 0 {
		s.RainDelayUntil = nil
	} else {
		t := now.Add(time.Duration(hours * float64(time.Hour))).UTC()
		s.RainDelayUntil = &t
	}
	if err := a.Store.SaveSettings(s); err != nil {
		return err
	}
	_ = a.Store.AddEvent(now, "rain_delay_set", map[string]any{"hours": hours})
	if err := a.Reload(); err != nil {
		return err
	}
	if hours > 0 {
		a.Runner.Block(now, core.OutcomeSkippedRain)
	}
	return nil
}

// Retention is how long finished runs and events are kept.
const Retention = 6 // months

// PruneTick drops history older than Retention, at most once a day and
// only with a trusted clock (a clock far in the future would wipe it).
func (a *App) PruneTick(now time.Time) {
	if !a.clockOK.Load() || (!a.lastPrune.IsZero() && now.Sub(a.lastPrune) < 24*time.Hour) {
		return
	}
	a.lastPrune = now
	runs, events, err := a.Store.Prune(now.AddDate(0, -Retention, 0))
	if err != nil {
		a.Log.Error("prune history", "err", err)
		return
	}
	if runs+events > 0 {
		a.Log.Info("pruned old history", "runs", runs, "events", events)
	}
}

// SnapshotTick copies the database to SnapshotDir once a local day, when
// the clock is trusted and nothing is watering or queued. A failure is
// logged and retried an hour later.
func (a *App) SnapshotTick(now time.Time) {
	if a.SnapshotDir == "" || !a.clockOK.Load() {
		return
	}
	_, _, _, loc := a.Snapshot()
	day := now.In(loc).Format("2006-01-02")
	a.snapMu.Lock()
	due := a.snapDay != day && (a.snapTry.IsZero() || now.Sub(a.snapTry) >= time.Hour || now.Before(a.snapTry))
	a.snapMu.Unlock()
	if !due {
		return
	}
	if _, err := os.Stat(filepath.Join(a.SnapshotDir, store.SnapshotName(day))); err == nil {
		a.snapMu.Lock()
		a.snapDay = day // already taken today (e.g. before a restart)
		a.snapMu.Unlock()
		return
	}
	if st := a.Runner.Snapshot(now); len(st.Running) > 0 || len(st.Queue) > 0 {
		return // wait for a quiet moment
	}
	keep := a.SnapshotKeep
	if keep <= 0 {
		keep = 7
	}
	path, err := a.Store.Snapshot(a.SnapshotDir, day, keep)
	a.snapMu.Lock()
	a.snapTry = now
	if err != nil {
		first := a.snapErr == ""
		a.snapErr = err.Error()
		a.snapMu.Unlock()
		a.Log.Error("database snapshot", "err", err)
		if first {
			_ = a.Store.AddEvent(now, "snapshot_failed", map[string]any{"error": err.Error()})
		}
		return
	}
	a.snapDay, a.snapErr = day, ""
	a.snapMu.Unlock()
	a.Log.Info("database snapshot", "file", path)
}

// SnapshotState is the last snapshot as the health check reports it.
type SnapshotState struct {
	Enabled bool      `json:"enabled"`
	Last    time.Time `json:"last,omitzero"`
	Error   string    `json:"error,omitempty"`
}

// SnapshotStatus reports the newest snapshot on disk and the last error.
func (a *App) SnapshotStatus() SnapshotState {
	if a.SnapshotDir == "" {
		return SnapshotState{}
	}
	out := SnapshotState{Enabled: true}
	if _, t, err := store.LatestSnapshot(a.SnapshotDir); err == nil && !t.IsZero() {
		out.Last = t.UTC()
	}
	a.snapMu.Lock()
	out.Error = a.snapErr
	a.snapMu.Unlock()
	return out
}

// Loop runs the planner and sensor until ctx is done.
func (a *App) Loop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := a.Clock()
			a.SensorTick(now)
			a.PlanTick(now)
			a.PruneTick(now)
			a.SnapshotTick(now)
		}
	}
}

// Upcoming returns the planned zone runs between from and until (at most
// limit; 0 = all), with estimated valve times given the current queue.
func (a *App) Upcoming(from, until time.Time, limit int) []schedule.PlannedRun {
	settings, zones, programs, loc := a.Snapshot()
	env := schedule.Env{Loc: loc, Lat: settings.Lat, Lon: settings.Lon}
	starts := schedule.Due(programs, env, from, until)
	zm := zoneMap(zones)
	byID := map[int64]core.Program{}
	for _, p := range programs {
		byID[p.ID] = p
	}
	skips, err := a.Store.Skips(from.Add(-time.Minute))
	if err != nil {
		a.Log.Error("load skips", "err", err)
	}
	var runs []schedule.PlannedRun
	for _, s := range starts {
		if skips[store.SkipKey{ProgramID: s.ProgramID, Start: s.Time.Unix()}] {
			for _, r := range schedule.Expand(byID[s.ProgramID], s.Time, zm, a.Conditions(s.Time)) {
				r.Skip = core.OutcomeSkippedUser
				runs = append(runs, r)
			}
			continue
		}
		c := a.Conditions(s.Time)
		if !c.RainDelay {
			c.SensorWet = false // the sensor state now says little about future days
			if s.Time.Sub(from) < 2*time.Hour {
				c.SensorWet = a.Conditions(from).SensorWet
			}
		}
		runs = append(runs, schedule.Expand(byID[s.ProgramID], s.Time, zm, c)...)
	}
	tl := schedule.Timeline(runs, zm, settings.StationDelayS, a.Runner.LaneFree(from))
	sort.SliceStable(tl, func(i, j int) bool { return tl[i].EstStart.Before(tl[j].EstStart) })
	if limit > 0 && len(tl) > limit {
		tl = tl[:limit]
	}
	return tl
}

// SensorState is the sensor as the UI shows it.
type SensorState struct {
	Enabled bool   `json:"enabled"`
	Wet     bool   `json:"wet"`
	Raw     bool   `json:"raw"`
	Error   string `json:"error,omitempty"`
	Sim     bool   `json:"sim"`
	Kind    string `json:"kind"`
}

// Sensor returns the current sensor state.
func (a *App) SensorState() SensorState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return SensorState{
		Enabled: a.settings.Sensor.Enabled, Wet: a.sensorWet, Raw: a.sensorRaw, Error: a.sensorErr,
		Sim: a.SimSensor != nil, Kind: a.settings.Sensor.Kind,
	}
}

// Subscribe returns a channel that receives a value after state changes.
func (a *App) Subscribe() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	a.subMu.Lock()
	a.subs[ch] = struct{}{}
	a.subMu.Unlock()
	return ch, func() {
		a.subMu.Lock()
		delete(a.subs, ch)
		a.subMu.Unlock()
	}
}

// Notify wakes all subscribers (non-blocking).
func (a *App) Notify() {
	a.subMu.Lock()
	defer a.subMu.Unlock()
	for ch := range a.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
