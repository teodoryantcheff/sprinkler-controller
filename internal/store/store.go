// Package store keeps all state in one SQLite file (WAL mode). Schema
// changes ship as numbered migrations inside the binary.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"sprinkler/internal/core"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

var migrations = []string{
	// 1: initial schema
	`
CREATE TABLE zones (
  id            INTEGER PRIMARY KEY,
  output_index  INTEGER NOT NULL UNIQUE,
  name          TEXT    NOT NULL,
  enabled       INTEGER NOT NULL DEFAULT 1,
  group_id      INTEGER NOT NULL DEFAULT 1,
  uses_master   INTEGER NOT NULL DEFAULT 0,
  ignore_rain   INTEGER NOT NULL DEFAULT 0,
  ignore_sensor INTEGER NOT NULL DEFAULT 0,
  max_minutes   INTEGER NOT NULL DEFAULT 120,
  sort_order    INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE programs (
  id             INTEGER PRIMARY KEY,
  name           TEXT    NOT NULL,
  enabled        INTEGER NOT NULL DEFAULT 1,
  day_rule       TEXT    NOT NULL,
  start_rule     TEXT    NOT NULL,
  season_from    TEXT    NOT NULL DEFAULT '',
  season_to      TEXT    NOT NULL DEFAULT '',
  weather_scaled INTEGER NOT NULL DEFAULT 1,
  sort_order     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE program_zones (
  program_id INTEGER NOT NULL REFERENCES programs(id) ON DELETE CASCADE,
  zone_id    INTEGER NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
  seconds    INTEGER NOT NULL,
  sort_order INTEGER NOT NULL,
  PRIMARY KEY (program_id, sort_order)
);
CREATE TABLE runs (
  id              INTEGER PRIMARY KEY,
  zone_id         INTEGER NOT NULL,
  program_id      INTEGER,
  planned_start   INTEGER NOT NULL,
  start           INTEGER,
  end             INTEGER,
  planned_seconds INTEGER NOT NULL,
  actual_seconds  INTEGER NOT NULL DEFAULT 0,
  scale_pct       INTEGER NOT NULL DEFAULT 100,
  litres          REAL,
  outcome         TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX runs_planned ON runs(planned_start);
CREATE INDEX runs_zone ON runs(zone_id, planned_start);
CREATE INDEX runs_program_start ON runs(program_id, planned_start);
CREATE TABLE events (
  id     INTEGER PRIMARY KEY,
  time   INTEGER NOT NULL,
  kind   TEXT    NOT NULL,
  detail TEXT    NOT NULL DEFAULT '{}'
);
CREATE INDEX events_time ON events(time);
CREATE TABLE weather_daily (
  date      TEXT NOT NULL,
  provider  TEXT NOT NULL,
  rain_mm   REAL,
  t_min     REAL,
  t_max     REAL,
  humidity  REAL,
  wind      REAL,
  et0_mm    REAL,
  scale_pct INTEGER,
  PRIMARY KEY (date, provider)
);
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  name          TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created       INTEGER NOT NULL
);
`,
}

// Store wraps the database.
type Store struct {
	DB *sql.DB
}

// Open opens (and migrates) the database at path.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; keeps the Pi 1 light
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) migrate() error {
	var v int
	if err := s.DB.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// ---- helpers

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func unix(t time.Time) int64 { return t.Unix() }

func tptr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0).UTC()
	return &t
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

// ---- settings

// Settings loads typed settings, starting from defaults.
func (s *Store) Settings() (core.Settings, error) {
	st := core.DefaultSettings()
	rows, err := s.DB.Query("SELECT key, value FROM settings")
	if err != nil {
		return st, err
	}
	defer rows.Close()
	m := map[string]json.RawMessage{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return st, err
		}
		m[k] = json.RawMessage(v)
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	// Re-marshal through the struct so unknown keys are ignored and
	// missing ones keep their defaults.
	cur, _ := json.Marshal(st)
	var base map[string]json.RawMessage
	_ = json.Unmarshal(cur, &base)
	for k, v := range m {
		// Only typed keys; raw settings (secrets) share the table.
		if _, known := base[k]; known && json.Valid(v) {
			base[k] = v
		}
	}
	all, _ := json.Marshal(base)
	if err := json.Unmarshal(all, &st); err != nil {
		return core.DefaultSettings(), fmt.Errorf("settings: %w", err)
	}
	return st, nil
}

// SaveSettings writes every field of st.
func (s *Store) SaveSettings(st core.Settings) error {
	b, _ := json.Marshal(st)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range m {
		if _, err := tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, string(v)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RawSetting reads a non-typed setting (secrets, internal state).
func (s *Store) RawSetting(key string) (string, error) {
	var v string
	err := s.DB.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetRawSetting writes a non-typed setting.
func (s *Store) SetRawSetting(key, value string) error {
	_, err := s.DB.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}

// ---- zones

const zoneCols = "id, output_index, name, enabled, group_id, uses_master, ignore_rain, ignore_sensor, max_minutes, sort_order"

func scanZone(sc interface{ Scan(...any) error }) (core.Zone, error) {
	var z core.Zone
	var en, um, ir, is int
	err := sc.Scan(&z.ID, &z.OutputIndex, &z.Name, &en, &z.GroupID, &um, &ir, &is, &z.MaxMinutes, &z.SortOrder)
	z.Enabled, z.UsesMaster, z.IgnoreRain, z.IgnoreSensor = en == 1, um == 1, ir == 1, is == 1
	return z, err
}

// Zones lists all zones in display order.
func (s *Store) Zones() ([]core.Zone, error) {
	rows, err := s.DB.Query("SELECT " + zoneCols + " FROM zones ORDER BY sort_order, output_index")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Zone
	for rows.Next() {
		z, err := scanZone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

// Zone loads one zone.
func (s *Store) Zone(id int64) (core.Zone, error) {
	return scanZone(s.DB.QueryRow("SELECT "+zoneCols+" FROM zones WHERE id=?", id))
}

// SaveZone inserts (ID == 0) or updates a zone.
func (s *Store) SaveZone(z *core.Zone) error {
	if z.MaxMinutes <= 0 {
		z.MaxMinutes = 120
	}
	if z.ID == 0 {
		res, err := s.DB.Exec(`INSERT INTO zones(output_index,name,enabled,group_id,uses_master,ignore_rain,ignore_sensor,max_minutes,sort_order)
			VALUES(?,?,?,?,?,?,?,?,?)`, z.OutputIndex, z.Name, b2i(z.Enabled), z.GroupID, b2i(z.UsesMaster), b2i(z.IgnoreRain), b2i(z.IgnoreSensor), z.MaxMinutes, z.SortOrder)
		if err != nil {
			return err
		}
		z.ID, err = res.LastInsertId()
		return err
	}
	_, err := s.DB.Exec(`UPDATE zones SET output_index=?,name=?,enabled=?,group_id=?,uses_master=?,ignore_rain=?,ignore_sensor=?,max_minutes=?,sort_order=? WHERE id=?`,
		z.OutputIndex, z.Name, b2i(z.Enabled), z.GroupID, b2i(z.UsesMaster), b2i(z.IgnoreRain), b2i(z.IgnoreSensor), z.MaxMinutes, z.SortOrder, z.ID)
	return err
}

// ---- programs

// Programs lists all programs with their zones.
func (s *Store) Programs() ([]core.Program, error) {
	rows, err := s.DB.Query("SELECT id,name,enabled,day_rule,start_rule,season_from,season_to,weather_scaled,sort_order FROM programs ORDER BY sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close() // also closed early below, before the next query
	var out []core.Program
	for rows.Next() {
		var p core.Program
		var en, ws int
		var dr, sr string
		if err := rows.Scan(&p.ID, &p.Name, &en, &dr, &sr, &p.SeasonFrom, &p.SeasonTo, &ws, &p.SortOrder); err != nil {
			return nil, err
		}
		p.Enabled, p.WeatherScaled = en == 1, ws == 1
		if err := json.Unmarshal([]byte(dr), &p.DayRule); err != nil {
			return nil, fmt.Errorf("program %d day_rule: %w", p.ID, err)
		}
		if err := json.Unmarshal([]byte(sr), &p.StartRule); err != nil {
			return nil, fmt.Errorf("program %d start_rule: %w", p.ID, err)
		}
		p.Zones = []core.ProgramZone{}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	idx := map[int64]int{}
	for i, p := range out {
		idx[p.ID] = i
	}
	zr, err := s.DB.Query("SELECT program_id, zone_id, seconds FROM program_zones ORDER BY program_id, sort_order")
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for zr.Next() {
		var pid int64
		var pz core.ProgramZone
		if err := zr.Scan(&pid, &pz.ZoneID, &pz.Seconds); err != nil {
			return nil, err
		}
		if i, ok := idx[pid]; ok {
			out[i].Zones = append(out[i].Zones, pz)
		}
	}
	return out, zr.Err()
}

// Program loads one program.
func (s *Store) Program(id int64) (core.Program, error) {
	ps, err := s.Programs()
	if err != nil {
		return core.Program{}, err
	}
	for _, p := range ps {
		if p.ID == id {
			return p, nil
		}
	}
	return core.Program{}, sql.ErrNoRows
}

// SaveProgram validates and inserts (ID == 0) or updates a program.
func (s *Store) SaveProgram(p *core.Program) error {
	if err := p.Validate(); err != nil {
		return err
	}
	dr, _ := json.Marshal(p.DayRule)
	sr, _ := json.Marshal(p.StartRule)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if p.ID == 0 {
		if p.SortOrder == 0 {
			_ = tx.QueryRow("SELECT COALESCE(MAX(sort_order),0)+1 FROM programs").Scan(&p.SortOrder)
		}
		res, err := tx.Exec(`INSERT INTO programs(name,enabled,day_rule,start_rule,season_from,season_to,weather_scaled,sort_order) VALUES(?,?,?,?,?,?,?,?)`,
			p.Name, b2i(p.Enabled), string(dr), string(sr), p.SeasonFrom, p.SeasonTo, b2i(p.WeatherScaled), p.SortOrder)
		if err != nil {
			return err
		}
		if p.ID, err = res.LastInsertId(); err != nil {
			return err
		}
	} else {
		res, err := tx.Exec(`UPDATE programs SET name=?,enabled=?,day_rule=?,start_rule=?,season_from=?,season_to=?,weather_scaled=?,sort_order=? WHERE id=?`,
			p.Name, b2i(p.Enabled), string(dr), string(sr), p.SeasonFrom, p.SeasonTo, b2i(p.WeatherScaled), p.SortOrder, p.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
		if _, err := tx.Exec("DELETE FROM program_zones WHERE program_id=?", p.ID); err != nil {
			return err
		}
	}
	for i, z := range p.Zones {
		if _, err := tx.Exec("INSERT INTO program_zones(program_id,zone_id,seconds,sort_order) VALUES(?,?,?,?)", p.ID, z.ZoneID, z.Seconds, i); err != nil {
			return fmt.Errorf("program zone %d: %w", z.ZoneID, err)
		}
	}
	return tx.Commit()
}

// DeleteProgram removes a program (its past runs keep the id).
func (s *Store) DeleteProgram(id int64) error {
	_, err := s.DB.Exec("DELETE FROM programs WHERE id=?", id)
	return err
}

// ---- runs

const runCols = "id, zone_id, program_id, planned_start, start, end, planned_seconds, actual_seconds, scale_pct, litres, outcome"

func scanRun(sc interface{ Scan(...any) error }) (core.Run, error) {
	var r core.Run
	var pid sql.NullInt64
	var ps int64
	var st, en sql.NullInt64
	var lit sql.NullFloat64
	err := sc.Scan(&r.ID, &r.ZoneID, &pid, &ps, &st, &en, &r.PlannedSeconds, &r.ActualSeconds, &r.ScalePct, &lit, &r.Outcome)
	if pid.Valid {
		v := pid.Int64
		r.ProgramID = &v
	}
	r.PlannedStart = time.Unix(ps, 0).UTC()
	r.Start, r.End = tptr(st), tptr(en)
	if lit.Valid {
		v := lit.Float64
		r.Litres = &v
	}
	return r, err
}

// InsertRun stores a new run and sets its ID.
func (s *Store) InsertRun(r *core.Run) error {
	res, err := s.DB.Exec(`INSERT INTO runs(zone_id,program_id,planned_start,start,end,planned_seconds,actual_seconds,scale_pct,litres,outcome) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		r.ZoneID, r.ProgramID, unix(r.PlannedStart), nullTime(r.Start), nullTime(r.End), r.PlannedSeconds, r.ActualSeconds, r.ScalePct, r.Litres, r.Outcome)
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

// UpdateRun writes the mutable parts of a run.
func (s *Store) UpdateRun(r *core.Run) error {
	_, err := s.DB.Exec(`UPDATE runs SET start=?, end=?, actual_seconds=?, outcome=?, litres=? WHERE id=?`,
		nullTime(r.Start), nullTime(r.End), r.ActualSeconds, r.Outcome, r.Litres, r.ID)
	return err
}

// ProgramStarted reports whether runs already exist for a program start
// (so the same planned start is never queued twice).
func (s *Store) ProgramStarted(programID int64, plannedStart time.Time) (bool, error) {
	var n int
	err := s.DB.QueryRow("SELECT COUNT(*) FROM runs WHERE program_id=? AND planned_start=?", programID, unix(plannedStart)).Scan(&n)
	return n > 0, err
}

// RunFilter selects runs for the history view.
type RunFilter struct {
	ZoneID    int64
	ProgramID int64 // -1 = manual only
	Outcome   string
	Before    time.Time
	After     time.Time
	Limit     int
}

// Runs lists runs newest first.
func (s *Store) Runs(f RunFilter) ([]core.Run, error) {
	var where []string
	var args []any
	where = append(where, "outcome != ''")
	if f.ZoneID > 0 {
		where = append(where, "zone_id=?")
		args = append(args, f.ZoneID)
	}
	if f.ProgramID > 0 {
		where = append(where, "program_id=?")
		args = append(args, f.ProgramID)
	} else if f.ProgramID == -1 {
		where = append(where, "program_id IS NULL")
	}
	if f.Outcome != "" {
		where = append(where, "outcome=?")
		args = append(args, f.Outcome)
	}
	if !f.Before.IsZero() {
		where = append(where, "planned_start < ?")
		args = append(args, unix(f.Before))
	}
	if !f.After.IsZero() {
		where = append(where, "planned_start >= ?")
		args = append(args, unix(f.After))
	}
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	// where holds only fixed clauses with ? placeholders.
	q := "SELECT " + runCols + " FROM runs WHERE " + strings.Join(where, " AND ") + " ORDER BY COALESCE(start, planned_start) DESC, id DESC LIMIT ?" //nolint:gosec // G202: no user input in the SQL text
	args = append(args, f.Limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OpenRuns returns runs with no outcome (queued or running when the
// process stopped).
func (s *Store) OpenRuns() ([]core.Run, error) {
	rows, err := s.DB.Query("SELECT " + runCols + " FROM runs WHERE outcome = ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ZoneSecondsSince sums actual watering per zone since t (for the daily cap).
func (s *Store) ZoneSecondsSince(t time.Time) (map[int64]int, error) {
	rows, err := s.DB.Query("SELECT zone_id, SUM(actual_seconds) FROM runs WHERE start >= ? GROUP BY zone_id", unix(t))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var z int64
		var n int
		if err := rows.Scan(&z, &n); err != nil {
			return nil, err
		}
		out[z] = n
	}
	return out, rows.Err()
}

// WeeklyMinutes returns watered minutes per zone per ISO week start (local
// Monday) for runs since t.
type WeekZone struct {
	Week    string  `json:"week"`
	ZoneID  int64   `json:"zone_id"`
	Minutes float64 `json:"minutes"`
}

// WeeklyMinutes groups watering by week and zone in loc.
func (s *Store) WeeklyMinutes(since time.Time, loc *time.Location) ([]WeekZone, error) {
	rows, err := s.DB.Query("SELECT zone_id, start, actual_seconds FROM runs WHERE start >= ? AND actual_seconds > 0", unix(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct {
		w string
		z int64
	}
	agg := map[key]float64{}
	for rows.Next() {
		var z, st int64
		var secs int
		if err := rows.Scan(&z, &st, &secs); err != nil {
			return nil, err
		}
		t := time.Unix(st, 0).In(loc)
		wd := (int(t.Weekday()) + 6) % 7
		monday := time.Date(t.Year(), t.Month(), t.Day()-wd, 0, 0, 0, 0, loc)
		agg[key{monday.Format("2006-01-02"), z}] += float64(secs) / 60
	}
	var out []WeekZone
	for k, v := range agg {
		out = append(out, WeekZone{Week: k.w, ZoneID: k.z, Minutes: v})
	}
	return out, rows.Err()
}

// ---- events

// AddEvent records an event; detail is marshalled to JSON.
func (s *Store) AddEvent(t time.Time, kind string, detail any) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	if detail == nil {
		b = []byte("{}")
	}
	_, err = s.DB.Exec("INSERT INTO events(time,kind,detail) VALUES(?,?,?)", unix(t), kind, string(b))
	return err
}

// Events lists events newest first.
func (s *Store) Events(before time.Time, limit int) ([]core.Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if before.IsZero() {
		before = time.Now().Add(time.Hour)
	}
	rows, err := s.DB.Query("SELECT id,time,kind,detail FROM events WHERE time < ? ORDER BY time DESC, id DESC LIMIT ?", unix(before), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Event
	for rows.Next() {
		var e core.Event
		var t int64
		var d string
		if err := rows.Scan(&e.ID, &t, &e.Kind, &d); err != nil {
			return nil, err
		}
		e.Time = time.Unix(t, 0).UTC()
		e.Detail = json.RawMessage(d)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- users

// User is an account.
type User struct {
	ID           int64
	Name         string
	PasswordHash string
}

// UserCount returns the number of users.
func (s *Store) UserCount() (int, error) {
	var n int
	err := s.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
	return n, err
}

// UserByName loads a user.
func (s *Store) UserByName(name string) (User, error) {
	var u User
	err := s.DB.QueryRow("SELECT id,name,password_hash FROM users WHERE name=?", name).Scan(&u.ID, &u.Name, &u.PasswordHash)
	return u, err
}

// SaveUser inserts or updates the password of a user.
func (s *Store) SaveUser(name, hash string) error {
	_, err := s.DB.Exec(`INSERT INTO users(name,password_hash,created) VALUES(?,?,?)
		ON CONFLICT(name) DO UPDATE SET password_hash=excluded.password_hash`, name, hash, time.Now().Unix())
	return err
}

// ---- backup

// Backup is the JSON export of configuration (not history).
type Backup struct {
	Version  int            `json:"version"`
	Exported time.Time      `json:"exported"`
	Settings core.Settings  `json:"settings"`
	Zones    []core.Zone    `json:"zones"`
	Programs []core.Program `json:"programs"`
}

// Export builds a backup.
func (s *Store) Export() (Backup, error) {
	st, err := s.Settings()
	if err != nil {
		return Backup{}, err
	}
	zs, err := s.Zones()
	if err != nil {
		return Backup{}, err
	}
	ps, err := s.Programs()
	if err != nil {
		return Backup{}, err
	}
	return Backup{Version: 1, Exported: time.Now().UTC(), Settings: st, Zones: zs, Programs: ps}, nil
}

// Restore replaces zones, programs and settings with the backup. Run
// history is kept.
func (s *Store) Restore(ctx context.Context, b Backup) error {
	if b.Version != 1 {
		return fmt.Errorf("unsupported backup version %d", b.Version)
	}
	for i := range b.Programs {
		if err := b.Programs[i].Validate(); err != nil {
			return fmt.Errorf("program %q: %w", b.Programs[i].Name, err)
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{"DELETE FROM program_zones", "DELETE FROM programs", "DELETE FROM zones"} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	for _, z := range b.Zones {
		if _, err := tx.Exec(`INSERT INTO zones(id,output_index,name,enabled,group_id,uses_master,ignore_rain,ignore_sensor,max_minutes,sort_order) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			z.ID, z.OutputIndex, z.Name, b2i(z.Enabled), z.GroupID, b2i(z.UsesMaster), b2i(z.IgnoreRain), b2i(z.IgnoreSensor), z.MaxMinutes, z.SortOrder); err != nil {
			return err
		}
	}
	for _, p := range b.Programs {
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
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.SaveSettings(b.Settings)
}
