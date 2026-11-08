package schedule

import (
	"testing"
	"time"

	"sprinkler/internal/core"
)

var sofia = mustLoc("Europe/Sofia")

func mustLoc(n string) *time.Location {
	l, err := time.LoadLocation(n)
	if err != nil {
		panic(err)
	}
	return l
}

var env = Env{Loc: sofia, Lat: 42.32, Lon: 24.04}

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, sofia)
	if err != nil {
		panic(err)
	}
	return t
}

func prog(day core.DayRule, start core.StartRule) core.Program {
	return core.Program{
		ID: 1, Name: "p", Enabled: true, DayRule: day, StartRule: start,
		Zones: []core.ProgramZone{{ZoneID: 1, Seconds: 600}},
	}
}

func fixed(times ...string) core.StartRule { return core.StartRule{Kind: "fixed", Times: times} }

func TestWeekdays(t *testing.T) {
	p := prog(core.DayRule{Kind: "weekdays", Days: []string{"mon", "wed", "fri", "sun"}}, fixed("06:00"))
	// 2026-11-02 is a Monday
	want := map[string]bool{"2026-11-01": true, "2026-11-02": true, "2026-11-03": false, "2026-11-04": true, "2026-11-07": false}
	for ds, w := range want {
		d, _ := ParseDate(ds)
		if got := DayMatches(p, d); got != w {
			t.Errorf("%s: got %v want %v", ds, got, w)
		}
	}
}

func TestInterval(t *testing.T) {
	p := prog(core.DayRule{Kind: "interval", Every: 3, Anchor: "2026-11-05"}, fixed("06:00"))
	for _, c := range []struct {
		d    string
		want bool
	}{{"2026-11-05", true}, {"2026-11-06", false}, {"2026-11-08", true}, {"2026-11-02", true}, {"2026-11-03", false}, {"2027-03-28", false}, {"2027-03-29", true}} {
		d, _ := ParseDate(c.d)
		if got := DayMatches(p, d); got != c.want {
			t.Errorf("%s: got %v want %v", c.d, got, c.want)
		}
	}
}

func TestOddEvenSkip31(t *testing.T) {
	odd := prog(core.DayRule{Kind: "oddeven", Parity: "odd", Skip31: true}, fixed("06:00"))
	even := prog(core.DayRule{Kind: "oddeven", Parity: "even"}, fixed("06:00"))
	cases := []struct {
		d         string
		odd, even bool
	}{
		{"2026-10-30", false, true},
		{"2026-10-31", false, false},
		{"2026-11-01", true, false},
		{"2028-02-29", false, false},
		{"2027-03-01", true, false},
	}
	for _, c := range cases {
		d, _ := ParseDate(c.d)
		if DayMatches(odd, d) != c.odd || DayMatches(even, d) != c.even {
			t.Errorf("%s: odd=%v even=%v", c.d, DayMatches(odd, d), DayMatches(even, d))
		}
	}
	odd.DayRule.Skip31 = false
	if d, _ := ParseDate("2026-10-31"); !DayMatches(odd, d) {
		t.Error("without skip31 the 31st is odd")
	}
	// parity as a restriction on a weekday rule
	wk := prog(core.DayRule{Kind: "weekdays", Days: []string{"mon", "tue"}, Parity: "odd"}, fixed("06:00"))
	if d, _ := ParseDate("2026-11-02"); DayMatches(wk, d) {
		t.Error("Mon 2nd is even, must not match")
	}
	if d, _ := ParseDate("2026-11-03"); !DayMatches(wk, d) {
		t.Error("Tue 3rd is odd, must match")
	}
}

func TestSeason(t *testing.T) {
	p := prog(core.DayRule{Kind: "oddeven", Parity: "odd"}, fixed("06:00"))
	p.SeasonFrom, p.SeasonTo = "04-01", "10-15"
	if d, _ := ParseDate("2026-10-15"); !DayMatches(p, d) {
		t.Error("15 Oct is odd and inside the season: expected a match")
	}
	if d, _ := ParseDate("2026-10-17"); DayMatches(p, d) {
		t.Error("17 Oct is after the season")
	}
}

func TestSeasonBounds(t *testing.T) {
	p := prog(core.DayRule{Kind: "weekdays", Days: []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}}, fixed("06:00"))
	p.SeasonFrom, p.SeasonTo = "04-01", "10-15"
	check := func(ds string, want bool) {
		d, _ := ParseDate(ds)
		if DayMatches(p, d) != want {
			t.Errorf("%s want %v", ds, want)
		}
	}
	check("2026-03-31", false)
	check("2026-04-01", true)
	check("2026-10-15", true)
	check("2026-10-16", false)
	p.SeasonFrom, p.SeasonTo = "11-01", "02-28" // wraps
	check("2026-12-25", true)
	check("2027-01-10", true)
	check("2026-06-01", false)
}

func TestSunTimes(t *testing.T) {
	// References: NOAA solar calculator equations (full form) for
	// lat 42.32, lon 24.04. OpenSprinkler's weather service reported
	// 07:05 / 17:12 for 2026-11-07, which agrees.
	cases := []struct {
		d         string
		rise, set string
	}{
		{"2026-11-07", "07:04", "17:10"},
		{"2026-06-21", "05:47", "21:04"},
		{"2026-12-21", "07:49", "16:54"},
		{"2027-03-28", "07:14", "19:45"}, // DST day
	}
	for _, c := range cases {
		d, _ := ParseDate(c.d)
		r, s, ok := SunTimes(d, env.Lat, env.Lon, sofia)
		if !ok {
			t.Fatal("no sunrise")
		}
		wr, ws := at(c.d+" "+c.rise), at(c.d+" "+c.set)
		if diff := r.Sub(wr); diff < -3*time.Minute || diff > 3*time.Minute {
			t.Errorf("%s sunrise %s, want %s ±3m", c.d, r.Format("15:04"), c.rise)
		}
		if diff := s.Sub(ws); diff < -3*time.Minute || diff > 3*time.Minute {
			t.Errorf("%s sunset %s, want %s ±3m", c.d, s.Format("15:04"), c.set)
		}
	}
}

func TestSolarStart(t *testing.T) {
	p := prog(core.DayRule{Kind: "oddeven", Parity: "odd"}, core.StartRule{Kind: "solar", Event: "sunrise", OffsetMin: -60})
	d, _ := ParseDate("2026-11-07")
	st := StartsOn(p, d, env)
	if len(st) != 1 {
		t.Fatalf("got %v", st)
	}
	rise, _, _ := SunTimes(d, env.Lat, env.Lon, sofia)
	if diff := rise.Sub(st[0]); diff < 59*time.Minute || diff > 61*time.Minute {
		t.Errorf("start %v, sunrise %v", st[0], rise)
	}
	// mixed fixed list with a solar entry
	p.StartRule = fixed("sunset+30", "05:00")
	st = StartsOn(p, d, env)
	if len(st) != 2 || st[0].Format("15:04") != "05:00" || st[1].Hour() != 17 {
		t.Errorf("mixed: %v", st)
	}
}

func TestDSTSpringGap(t *testing.T) {
	// 2027-03-28: clocks go 03:00 EET -> 04:00 EEST in Sofia.
	p := prog(core.DayRule{Kind: "interval", Every: 1, Anchor: "2027-01-01"}, fixed("03:30", "04:00", "02:59"))
	d, _ := ParseDate("2027-03-28")
	st := StartsOn(p, d, env)
	if len(st) != 2 {
		t.Fatalf("want 2 starts (03:30 and 04:00 collapse), got %v", st)
	}
	if st[0].Format("15:04 MST") != "02:59 EET" || st[1].Format("15:04 MST") != "04:00 EEST" {
		t.Errorf("got %v", st)
	}
	// Single 03:30 start runs at the first valid minute.
	p.StartRule = fixed("03:30")
	st = StartsOn(p, d, env)
	if len(st) != 1 || st[0].Format("15:04 MST") != "04:00 EEST" {
		t.Errorf("gap start: %v", st)
	}
}

func TestDSTFallRepeatedHour(t *testing.T) {
	// 2026-10-25: clocks go 04:00 EEST -> 03:00 EET; 03:30 happens twice.
	p := prog(core.DayRule{Kind: "interval", Every: 1, Anchor: "2026-01-01"}, fixed("03:30"))
	from := at("2026-10-24 12:00")
	to := at("2026-10-25 12:00")
	due := Due([]core.Program{p}, env, from, to)
	if len(due) != 1 {
		t.Fatalf("want exactly one start, got %v", due)
	}
	if due[0].Time.Format("15:04 MST") != "03:30 EEST" {
		t.Errorf("want first occurrence, got %v", due[0].Time)
	}
}

func TestRepeatAcrossMidnight(t *testing.T) {
	p := prog(core.DayRule{Kind: "weekdays", Days: []string{"sat"}},
		core.StartRule{Kind: "repeat", First: "22:00", EveryMin: 90, Count: 3})
	// Sat 2026-11-07 22:00, 23:30, Sun 01:00, 02:30
	due := Due([]core.Program{p}, env, at("2026-11-07 00:00"), at("2026-11-09 00:00"))
	var got []string
	for _, s := range due {
		got = append(got, s.Time.Format("01-02 15:04"))
	}
	want := []string{"11-07 22:00", "11-07 23:30", "11-08 01:00", "11-08 02:30"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v want %v", got, want)
		}
	}
}

func TestDueWindowsDoNotDoubleCount(t *testing.T) {
	p := prog(core.DayRule{Kind: "interval", Every: 1, Anchor: "2026-01-01"}, fixed("06:00", "19:30"))
	n := 0
	from := at("2026-11-07 00:00")
	for i := 0; i < 48*60; i++ {
		to := from.Add(time.Minute)
		n += len(Due([]core.Program{p}, env, from, to))
		from = to
	}
	if n != 4 {
		t.Errorf("want 4 starts in two days of minute ticks, got %d", n)
	}
}

func TestDisabledProgram(t *testing.T) {
	p := prog(core.DayRule{Kind: "interval", Every: 1, Anchor: "2026-01-01"}, fixed("06:00"))
	p.Enabled = false
	if len(Due([]core.Program{p}, env, at("2026-11-07 00:00"), at("2026-11-08 00:00"))) != 0 {
		t.Error("disabled program must not start")
	}
}

func TestExpandScalingAndBlocking(t *testing.T) {
	zones := map[int64]core.Zone{
		1: {ID: 1, Enabled: true, GroupID: 1},
		2: {ID: 2, Enabled: true, GroupID: 1, IgnoreRain: true},
		3: {ID: 3, Enabled: false, GroupID: 1},
		4: {ID: 4, Enabled: true, GroupID: 1},
	}
	p := core.Program{ID: 7, Enabled: true, WeatherScaled: true, Zones: []core.ProgramZone{
		{ZoneID: 1, Seconds: 1800}, {ZoneID: 2, Seconds: 1000}, {ZoneID: 3, Seconds: 60}, {ZoneID: 4, Seconds: 15},
	}}
	now := at("2026-11-07 06:00")
	runs := Expand(p, now, zones, Conditions{WateringLevelPct: 50})
	if len(runs) != 3 {
		t.Fatalf("disabled zone must be dropped: %v", runs)
	}
	if runs[0].Seconds != 900 || runs[1].Seconds != 500 || runs[2].Skip != core.OutcomeSkippedShort {
		t.Errorf("scaling: %+v", runs)
	}
	runs = Expand(p, now, zones, Conditions{WateringLevelPct: 100, RainDelay: true})
	if runs[0].Skip != core.OutcomeSkippedRain || runs[1].Skip != "" {
		t.Errorf("rain delay: %+v", runs)
	}
	runs = Expand(p, now, zones, Conditions{WateringLevelPct: 300, SensorWet: true})
	if runs[0].Skip != core.OutcomeSkippedSensor || runs[0].ScalePct != 250 {
		t.Errorf("sensor/cap: %+v", runs)
	}
	p.WeatherScaled = false
	runs = Expand(p, now, zones, Conditions{WateringLevelPct: 10})
	if runs[0].Seconds != 1800 {
		t.Errorf("unscaled program must ignore watering level: %+v", runs[0])
	}
}

func TestTimeline(t *testing.T) {
	zones := map[int64]core.Zone{
		1: {ID: 1, Enabled: true, GroupID: 1},
		2: {ID: 2, Enabled: true, GroupID: 1},
		3: {ID: 3, Enabled: true}, // independent
	}
	now := at("2026-11-07 06:00")
	runs := []PlannedRun{
		{ZoneID: 1, PlannedStart: now, Seconds: 1800},
		{ZoneID: 2, PlannedStart: now, Seconds: 600},
		{ZoneID: 3, PlannedStart: now, Seconds: 600},
	}
	tl := Timeline(runs, zones, 10, nil)
	if !tl[0].EstStart.Equal(now) || !tl[1].EstStart.Equal(now.Add(1810*time.Second)) || !tl[2].EstStart.Equal(now) {
		t.Errorf("%+v", tl)
	}
}
