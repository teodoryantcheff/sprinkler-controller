// Package schedule is the pure planner: given programs, zones, settings and
// a time window it returns the program starts and zone runs that fall in it.
// Nothing here reads the clock, the database or the hardware.
package schedule

import (
	"math"
	"sort"
	"time"

	"sprinkler/internal/core"
)

// Env is the place the planner works in.
type Env struct {
	Loc *time.Location
	Lat float64
	Lon float64
}

// Date is a local calendar date.
type Date struct {
	Y int
	M time.Month
	D int
}

// DateOf returns the local date of t in loc.
func DateOf(t time.Time, loc *time.Location) Date {
	y, m, d := t.In(loc).Date()
	return Date{y, m, d}
}

// AddDays moves a date by n days.
func (d Date) AddDays(n int) Date {
	t := time.Date(d.Y, d.M, d.D+n, 12, 0, 0, 0, time.UTC)
	return Date{t.Year(), t.Month(), t.Day()}
}

func (d Date) civil() time.Time { return time.Date(d.Y, d.M, d.D, 0, 0, 0, 0, time.UTC) }

// Before reports whether d is before o.
func (d Date) Before(o Date) bool { return d.civil().Before(o.civil()) }

func (d Date) String() string { return d.civil().Format("2006-01-02") }

// ParseDate parses YYYY-MM-DD.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Date{}, err
	}
	return Date{t.Year(), t.Month(), t.Day()}, nil
}

// DaysBetween returns b - a in whole days.
func DaysBetween(a, b Date) int {
	return int(math.Round(b.civil().Sub(a.civil()).Hours() / 24))
}

// DayMatches reports whether program p waters on local date d.
func DayMatches(p core.Program, d Date) bool {
	if !inSeason(p, d) {
		return false
	}
	r := p.DayRule
	switch r.Kind {
	case "weekdays":
		wd := core.WeekdayName(d.civil().Weekday())
		found := false
		for _, x := range r.Days {
			if x == wd {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	case "interval":
		a, err := ParseDate(r.Anchor)
		if err != nil || r.Every < 1 {
			return false
		}
		n := DaysBetween(a, d) % r.Every
		if n != 0 {
			return false
		}
	case "oddeven":
		// parity handled below
	default:
		return false
	}
	switch r.Parity {
	case "odd":
		if d.D%2 != 1 {
			return false
		}
		if r.Skip31 && (d.D == 31 || (d.M == time.February && d.D == 29)) {
			return false
		}
	case "even":
		if d.D%2 != 0 {
			return false
		}
	}
	return true
}

func inSeason(p core.Program, d Date) bool {
	if p.SeasonFrom == "" || p.SeasonTo == "" {
		return true
	}
	f, err1 := time.Parse("01-02", p.SeasonFrom)
	t, err2 := time.Parse("01-02", p.SeasonTo)
	if err1 != nil || err2 != nil {
		return true
	}
	md := int(d.M)*100 + d.D
	from := int(f.Month())*100 + f.Day()
	to := int(t.Month())*100 + t.Day()
	if from <= to {
		return md >= from && md <= to
	}
	return md >= from || md <= to // season wraps over new year
}

// StartMinutes returns the start minutes (relative to local midnight of d;
// may exceed 1439 for repeats that cross midnight) of p on date d,
// ignoring the day rule.
func StartMinutes(p core.Program, d Date, env Env) []int {
	resolve := func(s string) (int, bool) {
		ts, err := core.ParseTimeSpec(s)
		if err != nil {
			return 0, false
		}
		if ts.Event == "" {
			return ts.Minute, true
		}
		return solarMinute(d, ts.Event, ts.Minute, env), true
	}
	var out []int
	r := p.StartRule
	switch r.Kind {
	case "fixed":
		for _, s := range r.Times {
			if m, ok := resolve(s); ok {
				out = append(out, m)
			}
		}
	case "solar":
		out = append(out, solarMinute(d, r.Event, r.OffsetMin, env))
	case "repeat":
		first, ok := resolve(r.First)
		if !ok {
			return nil
		}
		out = append(out, first)
		if r.EveryMin > 0 {
			for i := 1; i <= r.Count; i++ {
				m := first + i*r.EveryMin
				if m >= 2*1440 {
					break
				}
				out = append(out, m)
			}
		}
	}
	sort.Ints(out)
	// drop duplicates
	uniq := out[:0]
	for i, m := range out {
		if i == 0 || m != out[i-1] {
			uniq = append(uniq, m)
		}
	}
	return uniq
}

// Resolve turns a local wall-clock time (date + minutes after midnight, may
// exceed a day) into an instant. A wall time inside a DST gap resolves to the
// first valid minute after the gap; a wall time that occurs twice resolves to
// the first occurrence.
func Resolve(d Date, minute int, loc *time.Location) time.Time {
	wall := d.civil().Add(time.Duration(minute) * time.Minute)
	for k := 0; k < 24*60; k++ {
		w := wall.Add(time.Duration(k) * time.Minute)
		var best time.Time
		for _, off := range offsetsNear(w, loc) {
			inst := w.Add(-time.Duration(off) * time.Second)
			lw := inst.In(loc)
			if lw.Year() == w.Year() && lw.Month() == w.Month() && lw.Day() == w.Day() &&
				lw.Hour() == w.Hour() && lw.Minute() == w.Minute() {
				if best.IsZero() || inst.Before(best) {
					best = inst
				}
			}
		}
		if !best.IsZero() {
			return best.In(loc)
		}
	}
	return wall // unreachable for real zones
}

func offsetsNear(w time.Time, loc *time.Location) []int {
	var offs []int
	for _, h := range []int{-26, -12, 0, 12, 26} {
		_, off := w.Add(time.Duration(h) * time.Hour).In(loc).Zone()
		dup := false
		for _, o := range offs {
			dup = dup || o == off
		}
		if !dup {
			offs = append(offs, off)
		}
	}
	return offs
}

// Start is one program start.
type Start struct {
	ProgramID int64     `json:"program_id"`
	Time      time.Time `json:"time"`
	Day       string    `json:"day"` // local date the start belongs to
}

// StartsOn returns the instants at which p starts for local day d (empty if
// the day does not match or the program is disabled).
func StartsOn(p core.Program, d Date, env Env) []time.Time {
	if !p.Enabled || !DayMatches(p, d) {
		return nil
	}
	var out []time.Time
	var last time.Time
	for _, m := range StartMinutes(p, d, env) {
		t := Resolve(d, m, env.Loc)
		if !last.IsZero() && !t.After(last) {
			continue // two wall times collapsed onto one instant (DST gap)
		}
		out = append(out, t)
		last = t
	}
	return out
}

// Due returns every program start with from < t <= to, sorted by time and
// program order.
func Due(progs []core.Program, env Env, from, to time.Time) []Start {
	if !to.After(from) {
		return nil
	}
	var out []Start
	first := DateOf(from, env.Loc).AddDays(-2) // repeats can spill past midnight
	last := DateOf(to, env.Loc)
	for _, p := range progs {
		for d := first; !last.Before(d); d = d.AddDays(1) {
			for _, t := range StartsOn(p, d, env) {
				if t.After(from) && !t.After(to) {
					out = append(out, Start{ProgramID: p.ID, Time: t, Day: d.String()})
				}
			}
		}
	}
	order := map[int64]int{}
	for _, p := range progs {
		order[p.ID] = p.SortOrder
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Time.Equal(out[j].Time) {
			return out[i].Time.Before(out[j].Time)
		}
		return order[out[i].ProgramID] < order[out[j].ProgramID]
	})
	return out
}

// Conditions are the facts that can block or scale runs at a start.
type Conditions struct {
	WateringLevelPct int
	RainDelay        bool
	SensorWet        bool
}

// PlannedRun is one zone run expanded from a program start.
type PlannedRun struct {
	ProgramID      int64     `json:"program_id"`
	ZoneID         int64     `json:"zone_id"`
	PlannedStart   time.Time `json:"planned_start"` // the program start
	PlannedSeconds int       `json:"planned_seconds"`
	Seconds        int       `json:"seconds"` // after scaling
	ScalePct       int       `json:"scale_pct"`
	Skip           string    `json:"skip,omitempty"` // outcome when the run will not happen
	EstStart       time.Time `json:"est_start"`      // estimated valve open time (Timeline)
}

// MinRunSeconds is the shortest scaled run that is still watered.
const MinRunSeconds = 10

// Expand turns a program start into zone runs, applying weather scaling
// and blocking. Disabled or unknown zones are left out.
func Expand(p core.Program, at time.Time, zones map[int64]core.Zone, c Conditions) []PlannedRun {
	scale := 100
	if p.WeatherScaled {
		scale = c.WateringLevelPct
		if scale < 0 {
			scale = 0
		}
		if scale > 250 {
			scale = 250
		}
	}
	var out []PlannedRun
	for _, pz := range p.Zones {
		z, ok := zones[pz.ZoneID]
		if !ok || !z.Enabled {
			continue
		}
		secs := int(math.Round(float64(pz.Seconds) * float64(scale) / 100))
		r := PlannedRun{
			ProgramID: p.ID, ZoneID: z.ID, PlannedStart: at,
			PlannedSeconds: pz.Seconds, Seconds: secs, ScalePct: scale,
		}
		switch {
		case c.RainDelay && !z.IgnoreRain:
			r.Skip = core.OutcomeSkippedRain
		case c.SensorWet && !z.IgnoreSensor:
			r.Skip = core.OutcomeSkippedSensor
		case secs < MinRunSeconds:
			r.Skip = core.OutcomeSkippedShort
		}
		out = append(out, r)
	}
	return out
}

// Timeline estimates when each run's valve opens, queueing runs per lane
// with delaySec between zones. laneFree gives the time each lane becomes
// free because of runs already in progress (may be nil).
func Timeline(runs []PlannedRun, zones map[int64]core.Zone, delaySec int, laneFree map[string]time.Time) []PlannedRun {
	free := map[string]time.Time{}
	for k, v := range laneFree {
		free[k] = v
	}
	out := make([]PlannedRun, len(runs))
	copy(out, runs)
	for i := range out {
		r := &out[i]
		if r.Skip != "" {
			r.EstStart = r.PlannedStart
			continue
		}
		lane := zones[r.ZoneID].Lane()
		st := r.PlannedStart
		if f, ok := free[lane]; ok && f.After(st) {
			st = f
		}
		r.EstStart = st
		free[lane] = st.Add(time.Duration(r.Seconds+delaySec) * time.Second)
	}
	return out
}
