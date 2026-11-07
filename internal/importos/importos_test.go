package importos

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sprinkler/internal/core"
	"sprinkler/internal/store"
)

// synthetic builds a minimal 2.1.8 nvm.dat with two stations and two programs.
func synthetic(t *testing.T) string {
	t.Helper()
	nvm := make([]byte, nvmSize)
	nvm[0], nvm[1] = 0, 2
	// program 0: enabled, weekly Mon+Wed, repeating start sunrise-60 once, zone 1 30 min
	p := nvm[2 : 2+programSize]
	p[0] = 0x01
	p[1] = 0x05
	binary.LittleEndian.PutUint16(p[4:], 0x503c)
	binary.LittleEndian.PutUint16(p[12:], 1800)
	copy(p[156:], "Lawn")
	// program 1: disabled, interval 2 days rem 1, odd, fixed 12:00 and 17:00, zone 2 7 min, weather
	p = nvm[2+programSize : 2+2*programSize]
	p[0] = 0x40 | 0x30 | 0x04 | 0x02
	p[1], p[2] = 1, 2
	binary.LittleEndian.PutUint16(p[4:], 720)
	binary.LittleEndian.PutUint16(p[6:], 1020)
	binary.LittleEndian.PutUint16(p[8:], 0xffff)
	binary.LittleEndian.PutUint16(p[10:], 0xffff)
	binary.LittleEndian.PutUint16(p[14:], 420)
	copy(p[156:], "--Grass")
	copy(nvm[addrLocation:], "42.32,24.04")
	copy(nvm[addrStnNames:], "Ливада Зона 1")
	copy(nvm[addrStnNames+stationNameSize:], "Туи ")
	nvm[addrStnDisable] = 0xa0 // stations 6 and 8 disabled
	for i := 0; i < 9; i++ {
		nvm[addrStnSeq+i] = 0xff
	}
	nvm[addrIgnRain] = 0x02 // station 2 ignores rain
	o := nvm[addrOptions:]
	o[optFWVersion] = 218
	o[optTimezone] = 56
	o[optStationDelay] = 122
	o[optMasterOnAdj], o[optMasterOffAdj] = 120, 120
	o[optSensor1Type], o[optSensor1Option] = 1, 1
	o[optWaterPct] = 100
	o[optDeviceEnable] = 1

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nvm.dat"), nvm, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(dir, "logs"), 0o755)
	// 2026-11-01 05:57:01 local (EET, +2) start, 1800 s, end written as local secs
	logs := "[0,\"wl\",100,1793512000]\n[1,0,1800,1793514421]\n[99,1,60,1793520000]\n[0,\"rs\",19,1793520100]\n"
	os.WriteFile(filepath.Join(dir, "logs", "20758.txt"), []byte(logs), 0o644)
	return dir
}

func TestParseSynthetic(t *testing.T) {
	sofia, _ := time.LoadLocation("Europe/Sofia")
	d, err := Parse(synthetic(t), Options{Timezone: "Europe/Sofia", Today: time.Date(2026, 11, 7, 12, 0, 0, 0, sofia)})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Zones) != 8 || d.Zones[0].Name != "Ливада Зона 1" || d.Zones[1].Name != "Туи" {
		t.Fatalf("zones: %+v", d.Zones[:2])
	}
	if d.Zones[5].Enabled || d.Zones[7].Enabled || !d.Zones[0].Enabled || d.Zones[0].GroupID != 1 {
		t.Errorf("zone flags wrong")
	}
	if !d.Zones[1].IgnoreRain || d.Zones[0].IgnoreRain {
		t.Errorf("ignore rain bits wrong")
	}
	s := d.Settings
	if s.StationDelayS != 10 || s.MasterZoneID != 0 || !s.Sensor.Enabled || s.Sensor.Normally != "open" || s.Lat != 42.32 {
		t.Errorf("settings: %+v", s)
	}
	p0, p1 := d.Programs[0], d.Programs[1]
	if !p0.Enabled || p0.WeatherScaled || p0.DayRule.Kind != "weekdays" || len(p0.DayRule.Days) != 2 || p0.DayRule.Days[1] != "wed" {
		t.Errorf("p0 day: %+v", p0)
	}
	if p0.StartRule.Kind != "solar" || p0.StartRule.Event != "sunrise" || p0.StartRule.OffsetMin != -60 {
		t.Errorf("p0 start: %+v", p0.StartRule)
	}
	if p1.Enabled || !p1.WeatherScaled || p1.DayRule.Kind != "interval" || p1.DayRule.Every != 2 || p1.DayRule.Parity != "odd" {
		t.Errorf("p1 day: %+v", p1)
	}
	// local epoch day of 2026-11-07 is 20764 (even) -> rem 1 => anchor 2026-11-06
	if p1.DayRule.Anchor != "2026-11-06" {
		t.Errorf("anchor %s", p1.DayRule.Anchor)
	}
	if p1.StartRule.Kind != "fixed" || len(p1.StartRule.Times) != 2 || p1.StartRule.Times[1] != "17:00" {
		t.Errorf("p1 start: %+v", p1.StartRule)
	}
	if len(p1.Zones) != 1 || p1.Zones[0].ZoneID != 2 || p1.Zones[0].Seconds != 420 {
		t.Errorf("p1 zones: %+v", p1.Zones)
	}
	if len(d.Runs) != 2 {
		t.Fatalf("runs: %+v", d.Runs)
	}
	r := d.Runs[0]
	if got := r.Start.In(sofia).Format("2006-01-02 15:04:05 MST"); got != "2026-11-01 05:57:01 EET" {
		t.Errorf("log time must be read as local wall clock, got %s", got)
	}
	if *r.ProgramID != 1 || r.ZoneID != 1 || d.Runs[1].ProgramID != nil {
		t.Errorf("run mapping: %+v", d.Runs)
	}
	if len(d.Events) != 2 {
		t.Errorf("events: %+v", d.Events)
	}
}

func TestImportIntoStore(t *testing.T) {
	d, err := Parse(synthetic(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := Import(ctx, s, d, false); err != nil {
		t.Fatal(err)
	}
	if err := Import(ctx, s, d, false); err == nil {
		t.Error("second import without force must fail")
	}
	if err := Import(ctx, s, d, true); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.Programs()
	zs, _ := s.Zones()
	runs, _ := s.Runs(store.RunFilter{Limit: 10})
	st, _ := s.Settings()
	if len(ps) != 2 || len(zs) != 8 || len(runs) != 2 || st.StationDelayS != 10 {
		t.Errorf("stored: %d programs %d zones %d runs %+v", len(ps), len(zs), len(runs), st)
	}
	if ps[0].Zones[0].Seconds != 1800 {
		t.Errorf("program zones: %+v", ps[0].Zones)
	}
}

// TestRealCopy runs against testdata/os (a copy pulled from the Pi, not in
// git) when present and prints the report for manual comparison.
func TestRealCopy(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "os")
	if _, err := os.Stat(filepath.Join(dir, "nvm.dat")); err != nil {
		t.Skip("no testdata/os copy")
	}
	sofia, _ := time.LoadLocation("Europe/Sofia")
	d, err := Parse(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + d.Report(sofia))
	if len(d.Zones) == 0 || len(d.Programs) == 0 {
		t.Error("nothing imported")
	}
	for _, p := range d.Programs {
		if err := p.Validate(); err != nil {
			t.Errorf("program %q invalid: %v", p.Name, err)
		}
	}
	_ = core.OutcomeDone
}
