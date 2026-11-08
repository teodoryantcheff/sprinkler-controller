package core

import (
	"testing"
	"time"
)

func TestZoneLaneAndValidate(t *testing.T) {
	if (Zone{ID: 3, GroupID: 2}).Lane() != "g2" || (Zone{ID: 3}).Lane() != "z3" {
		t.Fatal("lane")
	}
	ok := Zone{Name: "Lawn", OutputIndex: 7, MaxMinutes: 120}
	if err := ok.Validate(8); err != nil {
		t.Fatal(err)
	}
	for name, mod := range map[string]func(*Zone){
		"blank name":    func(z *Zone) { z.Name = "  " },
		"output high":   func(z *Zone) { z.OutputIndex = 8 },
		"output neg":    func(z *Zone) { z.OutputIndex = -1 },
		"max zero":      func(z *Zone) { z.MaxMinutes = 0 },
		"max too long":  func(z *Zone) { z.MaxMinutes = 721 },
		"group too big": func(z *Zone) { z.GroupID = 100 },
	} {
		z := ok
		mod(&z)
		if err := z.Validate(8); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestSettingsValidate(t *testing.T) {
	def := DefaultSettings()
	if err := def.Validate(); err != nil {
		t.Fatalf("defaults must be valid: %v", err)
	}
	if loc, err := def.Location(); err != nil || loc.String() != "Europe/Sofia" {
		t.Fatalf("location %v %v", loc, err)
	}
	for name, mod := range map[string]func(*Settings){
		"tz empty":       func(s *Settings) { s.Timezone = "" },
		"tz unknown":     func(s *Settings) { s.Timezone = "Mars/Olympus" },
		"lat":            func(s *Settings) { s.Lat = 91 },
		"lon":            func(s *Settings) { s.Lon = -181 },
		"level":          func(s *Settings) { s.WateringLevelPct = 251 },
		"station delay":  func(s *Settings) { s.StationDelayS = 601 },
		"master on":      func(s *Settings) { s.MasterOnDelayS = -1 },
		"master off":     func(s *Settings) { s.MasterOffDelayS = 601 },
		"max open":       func(s *Settings) { s.MaxOpenZones = 0 },
		"daily cap":      func(s *Settings) { s.DailyCapMinutes = 1441 },
		"sensor normal":  func(s *Settings) { s.Sensor.Normally = "maybe" },
		"sensor kind":    func(s *Settings) { s.Sensor.Kind = "flow" },
		"sensor delay":   func(s *Settings) { s.Sensor.OnDelayS = 86401 },
		"language":       func(s *Settings) { s.Language = "de" },
		"master zone id": func(s *Settings) { s.MasterZoneID = -1 },
	} {
		s := def
		mod(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestParseClock(t *testing.T) {
	for in, want := range map[string]int{"00:00": 0, "06:30": 390, " 23:59 ": 1439} {
		if got, err := ParseClock(in); err != nil || got != want {
			t.Errorf("%q: %d %v", in, got, err)
		}
	}
	for _, in := range []string{"", "6", "24:00", "12:60", "-1:00", "ab:cd"} {
		if _, err := ParseClock(in); err == nil {
			t.Errorf("%q: want error", in)
		}
	}
}

func TestParseTimeSpec(t *testing.T) {
	for in, want := range map[string]TimeSpec{
		"06:05":      {Minute: 365},
		"sunrise":    {Event: "sunrise"},
		"sunrise-30": {Event: "sunrise", Minute: -30},
		"sunset+15":  {Event: "sunset", Minute: 15},
	} {
		got, err := ParseTimeSpec(in)
		if err != nil || got != want {
			t.Errorf("%q: %+v %v", in, got, err)
		}
		if got.String() != in {
			t.Errorf("String() %q, want %q", got.String(), in)
		}
	}
	for _, in := range []string{"sunrise-", "sunset+721", "noon", "sunrisex"} {
		if _, err := ParseTimeSpec(in); err == nil {
			t.Errorf("%q: want error", in)
		}
	}
	if WeekdayName(time.Monday) != "mon" || WeekdayName(time.Sunday) != "sun" {
		t.Error("weekday names")
	}
}

func validProgram() Program {
	return Program{
		Name:      "Morning",
		DayRule:   DayRule{Kind: "weekdays", Days: []string{"mon", "thu"}},
		StartRule: StartRule{Kind: "fixed", Times: []string{"06:00", "sunset-60"}},
		Zones:     []ProgramZone{{ZoneID: 1, Seconds: 600}},
	}
}

func TestProgramValidateAccepts(t *testing.T) {
	for name, mod := range map[string]func(*Program){
		"weekdays":        func(*Program) {},
		"interval":        func(p *Program) { p.DayRule = DayRule{Kind: "interval", Every: 3, Anchor: "2026-04-01"} },
		"oddeven":         func(p *Program) { p.DayRule = DayRule{Kind: "oddeven", Parity: "odd", Skip31: true} },
		"weekdays+parity": func(p *Program) { p.DayRule.Parity = "even" },
		"repeat":          func(p *Program) { p.StartRule = StartRule{Kind: "repeat", First: "05:00", EveryMin: 120, Count: 3} },
		"solar":           func(p *Program) { p.StartRule = StartRule{Kind: "solar", Event: "sunrise", OffsetMin: -30} },
		"season":          func(p *Program) { p.SeasonFrom, p.SeasonTo = "04-01", "10-15" },
		"no zones":        func(p *Program) { p.Zones = nil },
	} {
		p := validProgram()
		mod(&p)
		if err := p.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestProgramValidateRejects(t *testing.T) {
	for name, mod := range map[string]func(*Program){
		"no name":          func(p *Program) { p.Name = "" },
		"no weekdays":      func(p *Program) { p.DayRule.Days = nil },
		"bad weekday":      func(p *Program) { p.DayRule.Days = []string{"monday"} },
		"interval 0":       func(p *Program) { p.DayRule = DayRule{Kind: "interval", Every: 0, Anchor: "2026-04-01"} },
		"interval 366":     func(p *Program) { p.DayRule = DayRule{Kind: "interval", Every: 366, Anchor: "2026-04-01"} },
		"interval no date": func(p *Program) { p.DayRule = DayRule{Kind: "interval", Every: 2} },
		"oddeven no par":   func(p *Program) { p.DayRule = DayRule{Kind: "oddeven"} },
		"bad parity":       func(p *Program) { p.DayRule.Parity = "prime" },
		"unknown day rule": func(p *Program) { p.DayRule.Kind = "monthly" },
		"no times":         func(p *Program) { p.StartRule.Times = nil },
		"five times":       func(p *Program) { p.StartRule.Times = []string{"1:00", "2:00", "3:00", "4:00", "5:00"} },
		"bad time":         func(p *Program) { p.StartRule.Times = []string{"25:00"} },
		"repeat bad first": func(p *Program) { p.StartRule = StartRule{Kind: "repeat", First: "x", EveryMin: 10} },
		"repeat every 0":   func(p *Program) { p.StartRule = StartRule{Kind: "repeat", First: "05:00", EveryMin: 0} },
		"repeat count":     func(p *Program) { p.StartRule = StartRule{Kind: "repeat", First: "05:00", EveryMin: 1, Count: 1441} },
		"solar event":      func(p *Program) { p.StartRule = StartRule{Kind: "solar", Event: "noon"} },
		"solar offset":     func(p *Program) { p.StartRule = StartRule{Kind: "solar", Event: "sunset", OffsetMin: 721} },
		"unknown start":    func(p *Program) { p.StartRule.Kind = "cron" },
		"season format":    func(p *Program) { p.SeasonFrom, p.SeasonTo = "2026-04-01", "10-15" },
		"season one side":  func(p *Program) { p.SeasonFrom = "04-01" },
		"zone 0 s":         func(p *Program) { p.Zones[0].Seconds = 0 },
		"zone over 24 h":   func(p *Program) { p.Zones[0].Seconds = 24*3600 + 1 },
	} {
		p := validProgram()
		p.Zones = append([]ProgramZone(nil), p.Zones...)
		mod(&p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
