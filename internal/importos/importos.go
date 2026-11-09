// Package importos reads OpenSprinkler 2.1.8 (Linux/OSPi build) data files
// — nvm.dat, stns.dat and logs/ — from a copy and converts them into our
// zones, programs, settings, runs and events.
//
// Layout reference: OpenSprinklerGen2 tag 218 (defines.h, program.h,
// OpenSprinkler.cpp, main.cpp). Never point this at the live controller's
// directory; it only reads, but work from a copy anyway.
package importos

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"sprinkler/internal/core"
	"sprinkler/internal/schedule"
	"sprinkler/internal/store"
)

// nvm.dat layout for RPI/BBB/LINUX, firmware 2.1.8.
const (
	nvmSize         = 8192
	maxExtBoards    = 8
	maxStations     = (1 + maxExtBoards) * 8 // 72
	stationNameSize = 24
	programDataSize = 6127
	programNameSize = 20
	programSize     = 176 // sizeof(ProgramStruct) on ARM EABI: 1+2+1(pad)+8+144+20
	addrNVCon       = programDataSize
	addrPassword    = addrNVCon + 12
	addrLocation    = addrPassword + 36
	addrJSURL       = addrLocation + 48
	addrWeatherURL  = addrJSURL + 48
	addrWeatherKey  = addrWeatherURL + 48
	addrStnNames    = addrWeatherKey + 24
	addrMasOp       = addrStnNames + maxStations*stationNameSize
	addrIgnRain     = addrMasOp + maxExtBoards + 1
	addrMasOp2      = addrIgnRain + maxExtBoards + 1
	addrStnDisable  = addrMasOp2 + maxExtBoards + 1
	addrStnSeq      = addrStnDisable + maxExtBoards + 1
	addrStnSpe      = addrStnSeq + maxExtBoards + 1
	addrOptions     = addrStnSpe + maxExtBoards + 1 // 8125
)

// Option indices (OS_OPTION_t in defines.h, 2.1.8).
const (
	optFWVersion     = 0
	optTimezone      = 1
	optHWVersion     = 14
	optExtBoards     = 15
	optStationDelay  = 17
	optMaster        = 18
	optMasterOnAdj   = 19
	optMasterOffAdj  = 20
	optSensor1Type   = 21
	optSensor1Option = 22
	optWaterPct      = 23
	optDeviceEnable  = 24
	optUseWeather    = 31
	optEnableLogging = 36
	optMaster2       = 37
	optSensor2Type   = 50
	numOptions       = 53
)

// Data is everything parsed from an OpenSprinkler directory.
type Data struct {
	FirmwareVersion int
	TZOffsetMin     int // OpenSprinkler's fixed offset from UTC
	Settings        core.Settings
	Zones           []core.Zone
	Programs        []core.Program
	Runs            []core.Run
	Events          []ImportedEvent
	Warnings        []string
}

// ImportedEvent is a non-run log line.
type ImportedEvent struct {
	Time   time.Time
	Kind   string
	Detail map[string]any
}

func (d *Data) warn(f string, a ...any) { d.Warnings = append(d.Warnings, fmt.Sprintf(f, a...)) }

// Options control conversion choices.
type Options struct {
	// Timezone is the zone OpenSprinkler's local clock followed. Its log
	// timestamps are local wall-clock seconds, not UTC.
	Timezone string
	// Today anchors interval programs (their phase is stored relative to
	// the epoch day; any matching date works as anchor).
	Today time.Time
}

// Parse reads dir/nvm.dat, dir/stns.dat (optional) and dir/logs/*.txt.
func Parse(dir string, opt Options) (*Data, error) {
	if opt.Timezone == "" {
		opt.Timezone = "Europe/Sofia"
	}
	loc, err := time.LoadLocation(opt.Timezone)
	if err != nil {
		return nil, err
	}
	if opt.Today.IsZero() {
		opt.Today = time.Now()
	}
	nvm, err := os.ReadFile(filepath.Join(dir, "nvm.dat"))
	if err != nil {
		return nil, err
	}
	if len(nvm) != nvmSize {
		return nil, fmt.Errorf("nvm.dat is %d bytes, want %d", len(nvm), nvmSize)
	}
	d := &Data{}
	opts := nvm[addrOptions : addrOptions+numOptions]
	d.FirmwareVersion = int(opts[optFWVersion])
	if d.FirmwareVersion != 218 {
		d.warn("firmware version %d in nvm.dat; layout assumed for 2.1.8 — check results", d.FirmwareVersion)
	}
	d.TZOffsetMin = (int(opts[optTimezone]) - 48) * 15
	if err := d.parseSettings(nvm, opts, loc); err != nil {
		return nil, err
	}
	d.parseZones(nvm, opts)
	d.parsePrograms(nvm, opt.Today.In(loc))
	d.fitCaps()
	if b, err := os.ReadFile(filepath.Join(dir, "stns.dat")); err == nil {
		d.checkSpecialStations(b, nvm)
	}
	if err := d.parseLogs(filepath.Join(dir, "logs"), loc); err != nil {
		return nil, err
	}
	return d, nil
}

func cstr(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}

func bit(b []byte, i int) bool { return b[i/8]&(1<<(i%8)) != 0 }

// waterTimeDecodeSigned mirrors utils.cpp: byte 0..240 -> -600..600 s.
func waterTimeDecodeSigned(v byte) int {
	if v > 240 {
		v = 240
	}
	return (int(v) - 120) * 5
}

func (d *Data) parseSettings(nvm, opts []byte, loc *time.Location) error {
	st := core.DefaultSettings()
	st.DeviceName = "sprinklerpi"
	st.Timezone = loc.String()
	if d.TZOffsetMin != 120 && d.TZOffsetMin != 180 {
		d.warn("OpenSprinkler time zone offset is %+d min, which is not Europe/Sofia", d.TZOffsetMin)
	}
	locStr := cstr(nvm[addrLocation : addrLocation+48])
	if la, lo, ok := strings.Cut(locStr, ","); ok {
		lat, err1 := strconv.ParseFloat(strings.TrimSpace(la), 64)
		lon, err2 := strconv.ParseFloat(strings.TrimSpace(lo), 64)
		if err1 == nil && err2 == nil {
			st.Lat, st.Lon = lat, lon
		} else {
			d.warn("location %q is not lat,lon; kept default", locStr)
		}
	} else {
		d.warn("location %q is not lat,lon; kept default", locStr)
	}
	st.StationDelayS = waterTimeDecodeSigned(opts[optStationDelay])
	if st.StationDelayS < 0 {
		d.warn("negative station delay %d s (overlap) not supported; set to 0", st.StationDelayS)
		st.StationDelayS = 0
	}
	if m := int(opts[optMaster]); m > 0 {
		st.MasterZoneID = int64(m) // zone ids are station index + 1
		st.MasterOnDelayS = waterTimeDecodeSigned(opts[optMasterOnAdj])
		st.MasterOffDelayS = waterTimeDecodeSigned(opts[optMasterOffAdj])
	}
	if opts[optMaster2] > 0 {
		d.warn("master 2 (station %d) is not supported and was ignored", opts[optMaster2])
	}
	st.WateringLevelPct = int(opts[optWaterPct])
	if opts[optUseWeather] != 0 {
		d.warn("weather adjustment method %d was on; imported as a fixed %d%% until weather lands in phase 2", opts[optUseWeather], st.WateringLevelPct)
	}
	st.ControllerOn = opts[optDeviceEnable] != 0
	switch opts[optSensor1Type] {
	case 0x00:
		st.Sensor.Enabled = false
	case 0x01:
		st.Sensor = core.SensorConfig{Enabled: true, Kind: "rain", Normally: "closed"}
		if opts[optSensor1Option] == 1 {
			st.Sensor.Normally = "open"
		}
	default:
		d.warn("sensor 1 type 0x%02x (flow/program switch) not imported", opts[optSensor1Type])
	}
	if opts[optSensor2Type] != 0 {
		d.warn("sensor 2 type 0x%02x not imported", opts[optSensor2Type])
	}
	// NVConData: sunrise, sunset (uint16), rd_stop_time (uint32, local secs)
	rd := binary.LittleEndian.Uint32(nvm[addrNVCon+4:])
	if rd > 0 {
		t := localEpoch(int64(rd), loc)
		st.RainDelayUntil = &t
	}
	d.Settings = st
	return nil
}

func (d *Data) parseZones(nvm, opts []byte) {
	n := (int(opts[optExtBoards]) + 1) * 8
	if n > maxStations {
		n = maxStations
	}
	mas := nvm[addrMasOp : addrMasOp+9]
	ign := nvm[addrIgnRain : addrIgnRain+9]
	dis := nvm[addrStnDisable : addrStnDisable+9]
	seq := nvm[addrStnSeq : addrStnSeq+9]
	master := int(opts[optMaster])
	for sid := 0; sid < n; sid++ {
		name := cstr(nvm[addrStnNames+sid*stationNameSize : addrStnNames+(sid+1)*stationNameSize])
		if name == "" {
			name = fmt.Sprintf("S%02d", sid+1)
		}
		z := core.Zone{
			ID:          int64(sid + 1),
			OutputIndex: sid,
			Name:        name,
			Enabled:     !bit(dis, sid),
			UsesMaster:  master > 0 && master != sid+1 && bit(mas, sid),
			IgnoreRain:  bit(ign, sid),
			MaxMinutes:  core.DefaultMaxMinutes,
			SortOrder:   sid,
		}
		// 2.1.8 has a single "ignore rain" bit that covers delay and sensor.
		z.IgnoreSensor = z.IgnoreRain
		if bit(seq, sid) {
			z.GroupID = 1
		}
		if master == sid+1 {
			z.Enabled = false // the master output is driven by the runner, not programs
		}
		d.Zones = append(d.Zones, z)
	}
}

func (d *Data) checkSpecialStations(stns, nvm []byte) {
	spe := nvm[addrStnSpe : addrStnSpe+9]
	const recSize = 248 // 1 type byte + STATION_SPECIAL_DATA_SIZE (255-8)
	for sid := 0; sid < len(d.Zones); sid++ {
		if !bit(spe, sid) {
			continue
		}
		typ := byte(0xff)
		if (sid+1)*recSize <= len(stns) {
			typ = stns[sid*recSize]
		}
		d.warn("zone %d (%s) is a special station (type %d); RF/remote/HTTP stations are not supported", sid+1, d.Zones[sid].Name, typ)
	}
}

// decodeStart mirrors ProgramStruct::starttime_decode: returns a time spec
// string, or "" for an unused slot.
func decodeStart(v int16) string {
	u := uint16(v) //nolint:gosec // G115: bit-for-bit reinterpretation of the stored field
	if u&0x8000 != 0 {
		return ""
	}
	off := int(u & 0x7ff)
	if u&(1<<12) != 0 {
		off = -off
	}
	switch {
	case u&(1<<14) != 0:
		return core.TimeSpec{Event: "sunrise", Minute: off}.String()
	case u&(1<<13) != 0:
		return core.TimeSpec{Event: "sunset", Minute: off}.String()
	}
	m := int(u & 0x7ff)
	if m >= 1440 {
		return ""
	}
	return core.TimeSpec{Minute: m}.String()
}

func (d *Data) parsePrograms(nvm []byte, today time.Time) {
	count := int(nvm[1])
	maxPrograms := (programDataSize - 2) / programSize
	if count > maxPrograms {
		d.warn("program count %d > max %d; truncated", count, maxPrograms)
		count = maxPrograms
	}
	days := []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	for i := 0; i < count; i++ {
		b := nvm[2+i*programSize : 2+(i+1)*programSize]
		flags := b[0]
		p := core.Program{
			ID:            int64(i + 1),
			Name:          cstr(b[156 : 156+programNameSize]),
			Enabled:       flags&0x01 != 0,
			WeatherScaled: flags&0x02 != 0,
			SortOrder:     i + 1,
		}
		oddeven := (flags >> 2) & 3
		typ := (flags >> 4) & 3
		fixedTimes := flags&0x40 != 0
		d0, d1 := b[1], b[2]
		switch typ {
		case 0: // weekly, bit 0 = Monday
			p.DayRule = core.DayRule{Kind: "weekdays"}
			for k := 0; k < 7; k++ {
				if d0&(1<<k) != 0 {
					p.DayRule.Days = append(p.DayRule.Days, days[k])
				}
			}
			if len(p.DayRule.Days) == 0 {
				d.warn("program %q has no weekdays; imported disabled", p.Name)
				p.DayRule.Days = []string{"mon"}
				p.Enabled = false
			}
		case 3: // interval: days[1] = every, days[0] = remainder of local epoch day
			every, rem := int(d1), int(d0)
			if every < 1 {
				every = 1
			}
			p.DayRule = core.DayRule{Kind: "interval", Every: every, Anchor: intervalAnchor(today, every, rem)}
		default:
			d.warn("program %q uses %s days, which is not supported; imported disabled as daily", p.Name, map[byte]string{1: "bi-weekly", 2: "monthly"}[typ])
			p.DayRule = core.DayRule{Kind: "interval", Every: 1, Anchor: today.Format("2006-01-02")}
			p.Enabled = false
		}
		switch oddeven {
		case 1:
			p.DayRule.Parity, p.DayRule.Skip31 = "odd", true
		case 2:
			p.DayRule.Parity = "even"
		}
		var st [4]int16
		for k := range st {
			st[k] = int16(binary.LittleEndian.Uint16(b[4+2*k:])) //nolint:gosec // G115: on-disk int16
		}
		if fixedTimes {
			var times []string
			for _, v := range st {
				if s := decodeStart(v); s != "" {
					times = append(times, s)
				}
			}
			p.StartRule = startRuleFor(times)
		} else {
			first := decodeStart(st[0])
			if first == "" {
				first = "00:00"
			}
			count, every := int(st[1]), int(st[2])
			if count > 0 && every > 0 {
				p.StartRule = core.StartRule{Kind: "repeat", First: first, EveryMin: every, Count: count}
			} else {
				p.StartRule = startRuleFor([]string{first})
			}
		}
		if len(p.StartRule.Times) == 0 && p.StartRule.Kind == "fixed" {
			d.warn("program %q has no start times; imported disabled", p.Name)
			p.StartRule.Times = []string{"06:00"}
			p.Enabled = false
		}
		for sid := 0; sid < len(d.Zones); sid++ {
			v := binary.LittleEndian.Uint16(b[12+2*sid:])
			switch {
			case v == 0:
			case v >= 65534:
				d.warn("program %q zone %d uses a sunrise/sunset-based duration; left out", p.Name, sid+1)
			default:
				p.Zones = append(p.Zones, core.ProgramZone{ZoneID: int64(sid + 1), Seconds: int(v)})
			}
		}
		if err := p.Validate(); err != nil {
			d.warn("program %q: %v", p.Name, err)
		}
		d.Programs = append(d.Programs, p)
	}
}

func startRuleFor(times []string) core.StartRule {
	if len(times) == 1 {
		if ts, err := core.ParseTimeSpec(times[0]); err == nil && ts.Event != "" {
			return core.StartRule{Kind: "solar", Event: ts.Event, OffsetMin: ts.Minute}
		}
	}
	return core.StartRule{Kind: "fixed", Times: times}
}

// intervalAnchor returns the latest date on or before today whose local
// epoch day number % every == rem.
func intervalAnchor(today time.Time, every, rem int) string {
	day := schedule.DaysBetween(schedule.Date{Y: 1970, M: 1, D: 1}, schedule.DateOf(today, today.Location()))
	back := ((day-rem)%every + every) % every
	return schedule.Date{Y: 1970, M: 1, D: 1}.AddDays(day - back).String()
}

// localEpoch converts OpenSprinkler's "local seconds since epoch" (now_tz)
// into an instant, reading the value as wall-clock time in loc.
func localEpoch(v int64, loc *time.Location) time.Time {
	w := time.Unix(v, 0).UTC()
	return time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), 0, loc).UTC()
}

func (d *Data) parseLogs(dir string, loc *time.Location) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil {
		return err
	}
	sort.Slice(files, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(files[i]), ".txt"))
		b, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(files[j]), ".txt"))
		return a < b
	})
	nprog := len(d.Programs)
	wl := -1
	bad := 0
	unknownProg := map[int]int{}
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var rec []json.RawMessage
			if err := json.Unmarshal([]byte(line), &rec); err != nil || len(rec) < 4 {
				bad++
				continue
			}
			var a0, a2, a3 int64
			if json.Unmarshal(rec[0], &a0) != nil || json.Unmarshal(rec[2], &a2) != nil || json.Unmarshal(rec[3], &a3) != nil {
				bad++
				continue
			}
			end := localEpoch(a3, loc)
			var sid int64
			if json.Unmarshal(rec[1], &sid) == nil {
				if int(sid) >= len(d.Zones) || a2 <= 0 {
					bad++
					continue
				}
				start := end.Add(-time.Duration(a2) * time.Second)
				r := core.Run{
					ZoneID: sid + 1, PlannedStart: start, Start: &start, End: ptr(end),
					PlannedSeconds: int(a2), ActualSeconds: int(a2), ScalePct: 100, Outcome: core.OutcomeDone,
				}
				if wl >= 0 {
					r.ScalePct = wl
				}
				switch {
				case a0 >= 1 && int(a0) <= nprog:
					pid := a0
					r.ProgramID = &pid
				case a0 == 99 || a0 == 254:
					// 99 = manual zone run, 254 = run-once program
				default:
					unknownProg[int(a0)]++
				}
				d.Runs = append(d.Runs, r)
				continue
			}
			var kind string
			_ = json.Unmarshal(rec[1], &kind)
			switch kind {
			case "wl":
				if int(a2) != wl {
					d.Events = append(d.Events, ImportedEvent{end, "watering_level", map[string]any{"pct": a2, "source": "opensprinkler"}})
					wl = int(a2)
				}
			case "rs":
				d.Events = append(d.Events, ImportedEvent{end, "sensor_wet_ended", map[string]any{"wet_seconds": a2, "since": end.Add(-time.Duration(a2) * time.Second), "source": "opensprinkler"}})
			case "rd":
				d.Events = append(d.Events, ImportedEvent{end, "rain_delay_ended", map[string]any{"seconds": a2, "source": "opensprinkler"}})
			case "fl":
				d.Events = append(d.Events, ImportedEvent{end, "flow", map[string]any{"pulses": a0, "seconds": a2, "source": "opensprinkler"}})
			default:
				bad++
			}
		}
		fh.Close()
		if err := sc.Err(); err != nil {
			return err
		}
	}
	if bad > 0 {
		d.warn("%d log lines could not be read and were skipped", bad)
	}
	for pid, n := range unknownProg {
		d.warn("%d runs reference program %d, which no longer exists; imported as manual", n, pid)
	}
	return nil
}

func ptr[T any](v T) *T { return &v }

// fitCaps raises a zone's hard cap where an imported program asks for
// longer than the default, so the cap never cuts a run the user set up.
func (d *Data) fitCaps() {
	for i := range d.Zones {
		z := &d.Zones[i]
		for _, p := range d.Programs {
			for _, pz := range p.Zones {
				if pz.ZoneID == z.ID {
					z.MaxMinutes = max(z.MaxMinutes, (pz.Seconds+59)/60)
				}
			}
		}
	}
}

// Import writes the data into an empty store. With force, existing zones,
// programs, runs and events are deleted first.
func Import(ctx context.Context, s *store.Store, d *Data, force bool) error {
	zs, err := s.Zones()
	if err != nil {
		return err
	}
	if len(zs) > 0 && !force {
		return fmt.Errorf("database already has %d zones; use -force to replace zones, programs and history", len(zs))
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{"DELETE FROM program_zones", "DELETE FROM programs", "DELETE FROM zones", "DELETE FROM runs", "DELETE FROM events"} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	b2i := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	for _, z := range d.Zones {
		if _, err := tx.Exec(`INSERT INTO zones(id,output_index,name,enabled,group_id,uses_master,ignore_rain,ignore_sensor,max_minutes,sort_order) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			z.ID, z.OutputIndex, z.Name, b2i(z.Enabled), z.GroupID, b2i(z.UsesMaster), b2i(z.IgnoreRain), b2i(z.IgnoreSensor), z.MaxMinutes, z.SortOrder); err != nil {
			return err
		}
	}
	for _, p := range d.Programs {
		dr, _ := json.Marshal(p.DayRule)
		sr, _ := json.Marshal(p.StartRule)
		if _, err := tx.Exec(`INSERT INTO programs(id,name,enabled,day_rule,start_rule,season_from,season_to,weather_scaled,sort_order) VALUES(?,?,?,?,?,?,?,?,?)`,
			p.ID, p.Name, b2i(p.Enabled), string(dr), string(sr), p.SeasonFrom, p.SeasonTo, b2i(p.WeatherScaled), p.SortOrder); err != nil {
			return err
		}
		for i, z := range p.Zones {
			if _, err := tx.Exec("INSERT INTO program_zones(program_id,zone_id,seconds,sort_order) VALUES(?,?,?,?)", p.ID, z.ZoneID, z.Seconds, i); err != nil {
				return err
			}
		}
	}
	for _, r := range d.Runs {
		if _, err := tx.Exec(`INSERT INTO runs(zone_id,program_id,planned_start,start,end,planned_seconds,actual_seconds,scale_pct,outcome) VALUES(?,?,?,?,?,?,?,?,?)`,
			r.ZoneID, r.ProgramID, r.PlannedStart.Unix(), r.Start.Unix(), r.End.Unix(), r.PlannedSeconds, r.ActualSeconds, r.ScalePct, r.Outcome); err != nil {
			return err
		}
	}
	for _, e := range d.Events {
		b, _ := json.Marshal(e.Detail)
		if _, err := tx.Exec("INSERT INTO events(time,kind,detail) VALUES(?,?,?)", e.Time.Unix(), e.Kind, string(b)); err != nil {
			return err
		}
	}
	summary, _ := json.Marshal(map[string]any{"zones": len(d.Zones), "programs": len(d.Programs), "runs": len(d.Runs), "events": len(d.Events), "warnings": d.Warnings})
	if _, err := tx.Exec("INSERT INTO events(time,kind,detail) VALUES(?,?,?)", time.Now().Unix(), "import_opensprinkler", string(summary)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.SaveSettings(d.Settings)
}

// Report is a human-readable summary for checking against the
// OpenSprinkler UI.
func (d *Data) Report(loc *time.Location) string {
	var b strings.Builder
	fmt.Fprintf(&b, "OpenSprinkler firmware %d, clock offset %+d min\n", d.FirmwareVersion, d.TZOffsetMin)
	s := d.Settings
	fmt.Fprintf(&b, "Location %.5f, %.5f · station delay %d s · watering level %d%% · controller %s\n",
		s.Lat, s.Lon, s.StationDelayS, s.WateringLevelPct, map[bool]string{true: "enabled", false: "disabled"}[s.ControllerOn])
	if s.MasterZoneID > 0 {
		fmt.Fprintf(&b, "Master valve: zone %d (on %+d s, off %+d s)\n", s.MasterZoneID, s.MasterOnDelayS, s.MasterOffDelayS)
	} else {
		fmt.Fprintf(&b, "Master valve: none\n")
	}
	if s.Sensor.Enabled {
		fmt.Fprintf(&b, "Sensor: %s, normally %s\n", s.Sensor.Kind, s.Sensor.Normally)
	}
	if s.RainDelayUntil != nil {
		fmt.Fprintf(&b, "Rain delay until %s\n", s.RainDelayUntil.In(loc).Format("2006-01-02 15:04"))
	}
	perZone := map[int64]int{}
	perZoneSecs := map[int64]int{}
	for _, r := range d.Runs {
		perZone[r.ZoneID]++
		perZoneSecs[r.ZoneID] += r.ActualSeconds
	}
	fmt.Fprintf(&b, "\nZones:\n")
	for _, z := range d.Zones {
		fmt.Fprintf(&b, "  %d  %-22s %-8s group=%d ignore_rain=%-5v runs=%-4d %6.1f h\n", z.ID, z.Name,
			map[bool]string{true: "enabled", false: "disabled"}[z.Enabled], z.GroupID, z.IgnoreRain, perZone[z.ID], float64(perZoneSecs[z.ID])/3600)
	}
	zn := map[int64]string{}
	for _, z := range d.Zones {
		zn[z.ID] = z.Name
	}
	perProg := map[int64]int{}
	for _, r := range d.Runs {
		if r.ProgramID != nil {
			perProg[*r.ProgramID]++
		}
	}
	fmt.Fprintf(&b, "\nPrograms:\n")
	for _, p := range d.Programs {
		dr, _ := json.Marshal(p.DayRule)
		sr, _ := json.Marshal(p.StartRule)
		var zl []string
		for _, z := range p.Zones {
			zl = append(zl, fmt.Sprintf("%s %dm", zn[z.ZoneID], z.Seconds/60))
		}
		fmt.Fprintf(&b, "  %d  %-22s %-8s scaled=%-5v runs=%-4d %s %s\n      %s\n", p.ID, p.Name,
			map[bool]string{true: "enabled", false: "disabled"}[p.Enabled], p.WeatherScaled, perProg[p.ID], dr, sr, strings.Join(zl, " → "))
	}
	if len(d.Runs) > 0 {
		first, last := d.Runs[0].Start.In(loc), d.Runs[len(d.Runs)-1].Start.In(loc)
		fmt.Fprintf(&b, "\nRuns: %d from %s to %s · events: %d\n", len(d.Runs), first.Format("2006-01-02"), last.Format("2006-01-02 15:04"), len(d.Events))
	}
	if len(d.Warnings) > 0 {
		fmt.Fprintf(&b, "\nWarnings:\n")
		for _, w := range d.Warnings {
			fmt.Fprintf(&b, "  - %s\n", w)
		}
	}
	return b.String()
}
