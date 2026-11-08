// Package core holds the domain types shared by every other package.
package core

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Zone is one valve output.
type Zone struct {
	ID           int64  `json:"id"`
	OutputIndex  int    `json:"output_index"` // 0-based bit / relay index
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	GroupID      int    `json:"group_id"` // 0 = runs independently (parallel)
	UsesMaster   bool   `json:"uses_master"`
	IgnoreRain   bool   `json:"ignore_rain"`
	IgnoreSensor bool   `json:"ignore_sensor"`
	MaxMinutes   int    `json:"max_minutes"`
	SortOrder    int    `json:"sort_order"`
}

// Lane is the sequential queue a zone belongs to: its group, or its own
// lane when it has no group.
func (z Zone) Lane() string {
	if z.GroupID > 0 {
		return "g" + strconv.Itoa(z.GroupID)
	}
	return "z" + strconv.FormatInt(z.ID, 10)
}

// DayRule decides on which local calendar days a program runs.
//
//	{"kind":"weekdays","days":["mon","wed"]}
//	{"kind":"interval","every":2,"anchor":"2026-04-01"}
//	{"kind":"oddeven","parity":"odd","skip31":true}
//
// Parity may also be set on weekdays/interval rules as an extra
// restriction (OpenSprinkler allows that combination).
type DayRule struct {
	Kind   string   `json:"kind"`
	Days   []string `json:"days,omitempty"`
	Every  int      `json:"every,omitempty"`
	Anchor string   `json:"anchor,omitempty"` // YYYY-MM-DD
	Parity string   `json:"parity,omitempty"` // odd | even
	Skip31 bool     `json:"skip31,omitempty"` // odd: skip 31st and 29 Feb
}

// StartRule decides at which times of a matching day a program starts.
//
//	{"kind":"fixed","times":["06:00","sunrise-30"]}
//	{"kind":"repeat","first":"05:00","every_min":120,"count":3}
//	{"kind":"solar","event":"sunrise","offset_min":-30}
//
// A time string is "HH:MM", "sunrise±N" or "sunset±N" (N in minutes).
type StartRule struct {
	Kind      string   `json:"kind"`
	Times     []string `json:"times,omitempty"`
	First     string   `json:"first,omitempty"`
	EveryMin  int      `json:"every_min,omitempty"`
	Count     int      `json:"count,omitempty"` // number of repeats after the first start
	Event     string   `json:"event,omitempty"` // sunrise | sunset
	OffsetMin int      `json:"offset_min,omitempty"`
}

// ProgramZone is one step of a program.
type ProgramZone struct {
	ZoneID  int64 `json:"zone_id"`
	Seconds int   `json:"seconds"`
}

// Program is a watering schedule.
type Program struct {
	ID            int64         `json:"id"`
	Name          string        `json:"name"`
	Enabled       bool          `json:"enabled"`
	DayRule       DayRule       `json:"day_rule"`
	StartRule     StartRule     `json:"start_rule"`
	SeasonFrom    string        `json:"season_from"` // MM-DD or ""
	SeasonTo      string        `json:"season_to"`   // MM-DD or ""
	WeatherScaled bool          `json:"weather_scaled"`
	SortOrder     int           `json:"sort_order"`
	Zones         []ProgramZone `json:"zones"`
}

// Outcome values for runs.
const (
	OutcomeDone          = "done"
	OutcomeStopped       = "stopped"
	OutcomeSkippedRain   = "skipped_rain"
	OutcomeSkippedSensor = "skipped_sensor"
	OutcomeSkippedShort  = "skipped_short"
	OutcomeSkippedDup    = "skipped_overlap"
	OutcomeCutMax        = "cut_max"
	OutcomeError         = "error"
)

// Run is one zone run, planned or done.
type Run struct {
	ID             int64      `json:"id"`
	ZoneID         int64      `json:"zone_id"`
	ProgramID      *int64     `json:"program_id"` // nil = manual
	PlannedStart   time.Time  `json:"planned_start"`
	Start          *time.Time `json:"start"`
	End            *time.Time `json:"end"`
	PlannedSeconds int        `json:"planned_seconds"`
	ActualSeconds  int        `json:"actual_seconds"`
	ScalePct       int        `json:"scale_pct"`
	Litres         *float64   `json:"litres"`
	Outcome        string     `json:"outcome"` // "" while queued/running
}

// Event is something worth keeping in the history besides runs.
type Event struct {
	ID     int64           `json:"id"`
	Time   time.Time       `json:"time"`
	Kind   string          `json:"kind"`
	Detail json.RawMessage `json:"detail"`
}

// SensorConfig describes the rain / soil sensor input behaviour.
type SensorConfig struct {
	Enabled   bool   `json:"enabled"`
	Kind      string `json:"kind"`     // rain | soil
	Normally  string `json:"normally"` // open | closed
	OnDelayS  int    `json:"on_delay_s"`
	OffDelayS int    `json:"off_delay_s"`
}

// Settings is the typed view of the settings table.
type Settings struct {
	DeviceName       string       `json:"device_name"`
	Timezone         string       `json:"timezone"`
	Lat              float64      `json:"lat"`
	Lon              float64      `json:"lon"`
	StationDelayS    int          `json:"station_delay_s"`
	MasterZoneID     int64        `json:"master_zone_id"` // 0 = none
	MasterOnDelayS   int          `json:"master_on_delay_s"`
	MasterOffDelayS  int          `json:"master_off_delay_s"`
	WateringLevelPct int          `json:"watering_level_pct"`
	RainDelayUntil   *time.Time   `json:"rain_delay_until"`
	Sensor           SensorConfig `json:"sensor"`
	MaxOpenZones     int          `json:"max_open_zones"`
	DailyCapMinutes  int          `json:"daily_cap_minutes"`
	LANNoLogin       bool         `json:"lan_no_login"`
	Language         string       `json:"language"`
	ControllerOn     bool         `json:"controller_on"` // false = scheduled programs don't start
}

// DefaultSettings for a fresh database.
func DefaultSettings() Settings {
	return Settings{
		DeviceName:       "Sprinkler",
		Timezone:         "Europe/Sofia",
		Lat:              42.32,
		Lon:              24.04,
		StationDelayS:    0,
		WateringLevelPct: 100,
		Sensor:           SensorConfig{Kind: "rain", Normally: "open"},
		MaxOpenZones:     2,
		DailyCapMinutes:  240,
		Language:         "en",
		ControllerOn:     true,
	}
}

// Location loads the configured time zone.
func (s Settings) Location() (*time.Location, error) {
	return time.LoadLocation(s.Timezone)
}

// ParseClock parses "HH:MM" into minutes after midnight.
func ParseClock(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, fmt.Errorf("bad time %q", s)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("bad time %q", s)
	}
	return hh*60 + mm, nil
}

// TimeSpec is a parsed start time string.
type TimeSpec struct {
	Event  string // "" for clock time, else sunrise | sunset
	Minute int    // clock minute (Event == "") or offset minutes
}

// ParseTimeSpec parses "HH:MM", "sunrise", "sunrise-30", "sunset+15".
func ParseTimeSpec(s string) (TimeSpec, error) {
	s = strings.TrimSpace(s)
	for _, ev := range []string{"sunrise", "sunset"} {
		if rest, ok := strings.CutPrefix(s, ev); ok {
			if rest == "" {
				return TimeSpec{Event: ev}, nil
			}
			n, err := strconv.Atoi(rest)
			if err != nil || n < -720 || n > 720 {
				return TimeSpec{}, fmt.Errorf("bad offset in %q", s)
			}
			return TimeSpec{Event: ev, Minute: n}, nil
		}
	}
	m, err := ParseClock(s)
	return TimeSpec{Minute: m}, err
}

func (t TimeSpec) String() string {
	if t.Event == "" {
		return fmt.Sprintf("%02d:%02d", t.Minute/60, t.Minute%60)
	}
	if t.Minute == 0 {
		return t.Event
	}
	return fmt.Sprintf("%s%+d", t.Event, t.Minute)
}

var weekdayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// WeekdayName returns the short lower-case name used in day rules.
func WeekdayName(d time.Weekday) string { return weekdayNames[d] }

// Validate checks a program's rules.
func (p *Program) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("name is required")
	}
	switch p.DayRule.Kind {
	case "weekdays":
		if len(p.DayRule.Days) == 0 {
			return fmt.Errorf("pick at least one weekday")
		}
		for _, d := range p.DayRule.Days {
			ok := false
			for _, n := range weekdayNames {
				ok = ok || d == n
			}
			if !ok {
				return fmt.Errorf("bad weekday %q", d)
			}
		}
	case "interval":
		if p.DayRule.Every < 1 || p.DayRule.Every > 365 {
			return fmt.Errorf("interval must be 1–365 days")
		}
		if _, err := time.Parse("2006-01-02", p.DayRule.Anchor); err != nil {
			return fmt.Errorf("interval needs a start date")
		}
	case "oddeven":
		if p.DayRule.Parity == "" {
			return fmt.Errorf("odd/even rule needs a parity")
		}
	default:
		return fmt.Errorf("unknown day rule %q", p.DayRule.Kind)
	}
	if pr := p.DayRule.Parity; pr != "" && pr != "odd" && pr != "even" {
		return fmt.Errorf("parity must be odd or even")
	}
	switch p.StartRule.Kind {
	case "fixed":
		if len(p.StartRule.Times) == 0 || len(p.StartRule.Times) > 4 {
			return fmt.Errorf("give 1–4 start times")
		}
		for _, t := range p.StartRule.Times {
			if _, err := ParseTimeSpec(t); err != nil {
				return err
			}
		}
	case "repeat":
		if _, err := ParseTimeSpec(p.StartRule.First); err != nil {
			return err
		}
		if p.StartRule.EveryMin < 1 || p.StartRule.Count < 0 || p.StartRule.Count > 1440 {
			return fmt.Errorf("repeat needs every ≥ 1 min and a count ≥ 0")
		}
	case "solar":
		if p.StartRule.Event != "sunrise" && p.StartRule.Event != "sunset" {
			return fmt.Errorf("solar event must be sunrise or sunset")
		}
		if p.StartRule.OffsetMin < -720 || p.StartRule.OffsetMin > 720 {
			return fmt.Errorf("offset out of range")
		}
	default:
		return fmt.Errorf("unknown start rule %q", p.StartRule.Kind)
	}
	for _, s := range []string{p.SeasonFrom, p.SeasonTo} {
		if s == "" {
			continue
		}
		if _, err := time.Parse("01-02", s); err != nil {
			return fmt.Errorf("season dates are MM-DD")
		}
	}
	if (p.SeasonFrom == "") != (p.SeasonTo == "") {
		return fmt.Errorf("season needs both dates")
	}
	for _, z := range p.Zones {
		if z.Seconds < 1 || z.Seconds > 24*3600 {
			return fmt.Errorf("zone durations must be 1 s – 24 h")
		}
	}
	return nil
}
