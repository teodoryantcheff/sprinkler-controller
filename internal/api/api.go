// Package api serves the JSON HTTP API, server-sent events and the
// embedded web UI.
package api

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"sprinkler/internal/app"
	"sprinkler/internal/core"
	"sprinkler/internal/runner"
	"sprinkler/internal/schedule"
	"sprinkler/internal/store"
)

//go:embed all:dist
var dist embed.FS

// Server is the HTTP handler.
type Server struct {
	app     *app.App
	log     *slog.Logger
	mux     *http.ServeMux
	outputs int
	caPEM   []byte
}

// SetCA makes the local CA certificate downloadable at /ca.crt, so phones
// can trust the HTTPS certificate.
func (s *Server) SetCA(pem []byte) { s.caPEM = pem }

func (s *Server) caCert(w http.ResponseWriter, r *http.Request) {
	if len(s.caPEM) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="sprinkler-ca.crt"`)
	w.Write(s.caPEM)
}

// New builds the handler.
func New(a *app.App, outputs int, log *slog.Logger) *Server {
	s := &Server{app: a, log: log, mux: http.NewServeMux(), outputs: outputs}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	s.mux.ServeHTTP(w, r)
}

type handler func(w http.ResponseWriter, r *http.Request) (any, error)

type httpError struct {
	code int
	msg  string
}

func (e httpError) Error() string { return e.msg }

func bad(f string, a ...any) error { return httpError{http.StatusBadRequest, fmt.Sprintf(f, a...)} }

// open wraps a handler that needs no login.
func (s *Server) open(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := h(w, r)
		s.reply(w, v, err)
	}
}

// auth wraps a handler that needs a session (or LAN access when enabled).
// Writes must be JSON, which together with SameSite=Strict cookies keeps
// cross-site form posts out.
func (s *Server) auth(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.authorized(r); !ok {
			s.reply(w, nil, httpError{http.StatusUnauthorized, errUnauthorized.Error()})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				s.reply(w, nil, httpError{http.StatusUnsupportedMediaType, "send JSON"})
				return
			}
		}
		v, err := h(w, r)
		s.reply(w, v, err)
	}
}

func (s *Server) reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		code := http.StatusInternalServerError
		var he httpError
		switch {
		case errors.As(err, &he):
			code = he.code
		case errors.Is(err, sql.ErrNoRows):
			code, err = http.StatusNotFound, errors.New("not found")
		case errors.Is(err, runner.ErrUnknownZone), errors.Is(err, runner.ErrZoneDisabled), errors.Is(err, runner.ErrAlreadyQueued),
			errors.Is(err, runner.ErrBadDuration), errors.Is(err, runner.ErrMasterZone):
			code = http.StatusBadRequest
		case errors.Is(err, runner.ErrFault):
			code = http.StatusConflict
		}
		if code == http.StatusInternalServerError {
			s.log.Error("api", "err", err)
		}
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if v == nil {
		v = map[string]bool{"ok": true}
	}
	json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return bad("bad JSON: %v", err)
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, bad("bad id")
	}
	return id, nil
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /ca.crt", s.caCert)
	m.HandleFunc("GET /api/me", s.open(s.me))
	m.HandleFunc("POST /api/setup", s.open(s.setup))
	m.HandleFunc("POST /api/login", s.open(s.login))
	m.HandleFunc("POST /api/logout", s.open(s.logout))
	m.HandleFunc("POST /api/password", s.auth(s.password))

	m.HandleFunc("GET /api/state", s.auth(s.state))
	m.HandleFunc("GET /api/stream", s.stream)
	m.HandleFunc("GET /api/info", s.auth(s.info))

	m.HandleFunc("GET /api/zones", s.auth(s.listZones))
	m.HandleFunc("POST /api/zones", s.auth(s.saveZone))
	m.HandleFunc("PUT /api/zones/{id}", s.auth(s.saveZone))

	m.HandleFunc("GET /api/programs", s.auth(s.listPrograms))
	m.HandleFunc("POST /api/programs", s.auth(s.saveProgram))
	m.HandleFunc("PUT /api/programs/{id}", s.auth(s.saveProgram))
	m.HandleFunc("DELETE /api/programs/{id}", s.auth(s.deleteProgram))
	m.HandleFunc("POST /api/programs/preview", s.auth(s.previewProgram))
	m.HandleFunc("POST /api/programs/{id}/run", s.auth(s.runProgram))
	m.HandleFunc("GET /api/upcoming", s.auth(s.upcoming))

	m.HandleFunc("POST /api/run/zone", s.auth(s.runZone))
	m.HandleFunc("POST /api/run/once", s.auth(s.runOnce))
	m.HandleFunc("POST /api/stop", s.auth(s.stop))
	m.HandleFunc("POST /api/pause", s.auth(s.pause))
	m.HandleFunc("POST /api/resume", s.auth(s.resume))
	m.HandleFunc("POST /api/fault/clear", s.auth(s.clearFault))
	m.HandleFunc("POST /api/raindelay", s.auth(s.rainDelay))
	m.HandleFunc("POST /api/sim/sensor", s.auth(s.simSensor))

	m.HandleFunc("GET /api/runs", s.auth(s.runs))
	m.HandleFunc("GET /api/history/weekly", s.auth(s.weekly))
	m.HandleFunc("GET /api/log", s.auth(s.eventLog))

	m.HandleFunc("GET /api/settings", s.auth(s.getSettings))
	m.HandleFunc("PUT /api/settings", s.auth(s.putSettings))
	m.HandleFunc("GET /api/backup", s.auth(s.backup))
	m.HandleFunc("POST /api/restore", s.auth(s.restore))

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		s.reply(w, nil, httpError{http.StatusNotFound, "no such endpoint"})
	})
	m.Handle("/", s.static())
}

// static serves the embedded UI; unknown paths get index.html (SPA).
func (s *Server) static() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><title>Sprinkler</title><p>The web UI is not built into this binary. Run <code>make web build</code>. The API is at <code>/api/</code>.`)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			r.URL.Path = "/"
			p = "index.html"
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

// ---- auth endpoints

func (s *Server) me(w http.ResponseWriter, r *http.Request) (any, error) {
	n, err := s.app.Store.UserCount()
	if err != nil {
		return nil, err
	}
	u, ok := s.authorized(r)
	st, _, _, _ := s.app.Snapshot()
	return map[string]any{"user": u, "logged_in": ok, "setup_required": n == 0, "language": st.Language, "device_name": st.DeviceName,
		"https": r.TLS != nil, "ca_available": len(s.caPEM) > 0}, nil
}

// MinPassword is the shortest accepted password, in characters (also used by "sprinkler passwd").
const MinPassword = 7

type creds struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) (any, error) {
	n, err := s.app.Store.UserCount()
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, httpError{http.StatusForbidden, "already set up"}
	}
	var c creds
	if err := decode(r, &c); err != nil {
		return nil, err
	}
	if len([]rune(c.Password)) < MinPassword {
		return nil, bad("password must be at least %d characters", MinPassword)
	}
	if c.Name == "" {
		c.Name = "admin"
	}
	h, err := HashPassword(c.Password)
	if err != nil {
		return nil, err
	}
	if err := s.app.Store.SaveUser(c.Name, h); err != nil {
		return nil, err
	}
	return nil, s.setSession(w, r, c.Name)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) (any, error) {
	var c creds
	if err := decode(r, &c); err != nil {
		return nil, err
	}
	if c.Name == "" {
		c.Name = "admin"
	}
	u, err := s.app.Store.UserByName(c.Name)
	if err != nil || !CheckPassword(u.PasswordHash, c.Password) {
		time.Sleep(700 * time.Millisecond)
		return nil, httpError{http.StatusUnauthorized, "wrong name or password"}
	}
	return nil, s.setSession(w, r, u.Name)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) (any, error) {
	clearSession(w)
	return nil, nil
}

func (s *Server) password(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	name := s.sessionUser(r)
	if name == "" {
		return nil, httpError{http.StatusForbidden, "log in with a password to change it"}
	}
	u, err := s.app.Store.UserByName(name)
	if err != nil || !CheckPassword(u.PasswordHash, req.Current) {
		return nil, httpError{http.StatusForbidden, "current password is wrong"}
	}
	if len([]rune(req.New)) < MinPassword {
		return nil, bad("password must be at least %d characters", MinPassword)
	}
	h, err := HashPassword(req.New)
	if err != nil {
		return nil, err
	}
	return nil, s.app.Store.SaveUser(name, h)
}

// ---- state

// Status is what the Home screen shows.
type Status struct {
	Now            time.Time             `json:"now"`
	Runner         runner.State          `json:"runner"`
	Next           []schedule.PlannedRun `json:"next"`
	WateringLevel  int                   `json:"watering_level"`
	WateringSource string                `json:"watering_source"`
	RainDelayUntil *time.Time            `json:"rain_delay_until"`
	Sensor         app.SensorState       `json:"sensor"`
	ControllerOn   bool                  `json:"controller_on"`
	Timezone       string                `json:"timezone"`
	Sun            map[string]time.Time  `json:"sun"`
}

func (s *Server) status() Status {
	now := s.app.Clock()
	st, _, _, loc := s.app.Snapshot()
	out := Status{
		Now:            now.UTC(),
		Runner:         s.app.Runner.Snapshot(now),
		Next:           []schedule.PlannedRun{},
		WateringLevel:  st.WateringLevelPct,
		WateringSource: "manual",
		Sensor:         s.app.SensorState(),
		ControllerOn:   st.ControllerOn,
		Timezone:       st.Timezone,
	}
	if st.RainDelayUntil != nil && now.Before(*st.RainDelayUntil) {
		out.RainDelayUntil = st.RainDelayUntil
	}
	for _, r := range s.app.Upcoming(now, now.Add(8*24*time.Hour), 0) {
		if r.Skip == "" && r.EstStart.After(now) {
			out.Next = append(out.Next, r)
			if len(out.Next) == 3 {
				break
			}
		}
	}
	if rise, set, ok := schedule.SunTimes(schedule.DateOf(now, loc), st.Lat, st.Lon, loc); ok {
		out.Sun = map[string]time.Time{"sunrise": rise.UTC(), "sunset": set.UTC()}
	}
	return out
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) (any, error) { return s.status(), nil }

// stream pushes the status as server-sent events on every change (at most
// once a second) and every 30 s.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorized(r); !ok {
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, cancel := s.app.Subscribe()
	defer cancel()
	send := func() bool {
		b, err := json.Marshal(s.status())
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: state\ndata: %s\n\n", b); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	if !send() {
		return
	}
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			time.Sleep(200 * time.Millisecond) // coalesce bursts
			select {
			case <-ch:
			default:
			}
			if !send() {
				return
			}
		case <-tick.C:
			if !send() {
				return
			}
		}
	}
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) (any, error) {
	host, _ := os.Hostname()
	st := s.app.Runner.Snapshot(s.app.Clock())
	return map[string]any{
		"version": s.app.Version, "driver": st.Driver, "outputs": s.outputs, "hostname": host,
		"go": runtime.Version(), "arch": runtime.GOARCH, "started": s.app.Started.UTC(),
	}, nil
}

// ---- zones

func (s *Server) listZones(w http.ResponseWriter, r *http.Request) (any, error) {
	zs, err := s.app.Store.Zones()
	if zs == nil {
		zs = []core.Zone{}
	}
	return zs, err
}

func (s *Server) saveZone(w http.ResponseWriter, r *http.Request) (any, error) {
	var z core.Zone
	if err := decode(r, &z); err != nil {
		return nil, err
	}
	if r.Method == http.MethodPut {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		if _, err := s.app.Store.Zone(id); err != nil {
			return nil, err
		}
		z.ID = id
	} else {
		z.ID = 0
	}
	z.Name = strings.TrimSpace(z.Name)
	if z.Name == "" {
		return nil, bad("name is required")
	}
	if z.OutputIndex < 0 || z.OutputIndex >= s.outputs {
		return nil, bad("output must be 1–%d", s.outputs)
	}
	if z.MaxMinutes < 1 || z.MaxMinutes > 720 {
		return nil, bad("max minutes must be 1–720")
	}
	if z.GroupID < 0 || z.GroupID > 99 {
		return nil, bad("group must be 0–99")
	}
	if err := s.app.Store.SaveZone(&z); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, bad("output %d is already used by another zone", z.OutputIndex+1)
		}
		return nil, err
	}
	return z, s.app.Reload()
}

// ---- programs

func (s *Server) listPrograms(w http.ResponseWriter, r *http.Request) (any, error) {
	ps, err := s.app.Store.Programs()
	if ps == nil {
		ps = []core.Program{}
	}
	return ps, err
}

func (s *Server) checkProgramZones(p *core.Program) error {
	_, zones, _, _ := s.app.Snapshot()
	known := map[int64]bool{}
	for _, z := range zones {
		known[z.ID] = true
	}
	st, _, _, _ := s.app.Snapshot()
	for _, pz := range p.Zones {
		if !known[pz.ZoneID] {
			return bad("unknown zone %d", pz.ZoneID)
		}
		if pz.ZoneID == st.MasterZoneID {
			return bad("the master valve cannot be part of a program")
		}
	}
	return nil
}

func (s *Server) saveProgram(w http.ResponseWriter, r *http.Request) (any, error) {
	var p core.Program
	if err := decode(r, &p); err != nil {
		return nil, err
	}
	p.ID = 0
	if r.Method == http.MethodPut {
		id, err := pathID(r)
		if err != nil {
			return nil, err
		}
		p.ID = id
	}
	p.Name = strings.TrimSpace(p.Name)
	if err := p.Validate(); err != nil {
		return nil, bad("%v", err)
	}
	if err := s.checkProgramZones(&p); err != nil {
		return nil, err
	}
	if err := s.app.Store.SaveProgram(&p); err != nil {
		return nil, err
	}
	return p, s.app.Reload()
}

func (s *Server) deleteProgram(w http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	if err := s.app.Store.DeleteProgram(id); err != nil {
		return nil, err
	}
	return nil, s.app.Reload()
}

// previewProgram returns the next 5 starts of an (unsaved) program with
// its zone runs at today's watering level.
func (s *Server) previewProgram(w http.ResponseWriter, r *http.Request) (any, error) {
	var p core.Program
	if err := decode(r, &p); err != nil {
		return nil, err
	}
	if p.Name == "" {
		p.Name = "preview"
	}
	if err := p.Validate(); err != nil {
		return nil, bad("%v", err)
	}
	p.Enabled = true
	st, zones, _, loc := s.app.Snapshot()
	env := schedule.Env{Loc: loc, Lat: st.Lat, Lon: st.Lon}
	now := s.app.Clock()
	zm := map[int64]core.Zone{}
	for _, z := range zones {
		zm[z.ID] = z
	}
	type startPreview struct {
		Time         time.Time             `json:"time"`
		Runs         []schedule.PlannedRun `json:"runs"`
		TotalSeconds int                   `json:"total_seconds"`
	}
	out := []startPreview{}
	starts := schedule.Due([]core.Program{p}, env, now, now.Add(400*24*time.Hour))
	for _, sp := range starts {
		runs := schedule.Timeline(schedule.Expand(p, sp.Time, zm, schedule.Conditions{WateringLevelPct: st.WateringLevelPct}), zm, st.StationDelayS, nil)
		total := 0
		for _, r := range runs {
			if r.Skip == "" {
				total += r.Seconds
			}
		}
		out = append(out, startPreview{Time: sp.Time.UTC(), Runs: runs, TotalSeconds: total})
		if len(out) == 5 {
			break
		}
	}
	return out, nil
}

func (s *Server) runProgram(w http.ResponseWriter, r *http.Request) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	ids, err := s.app.RunProgramNow(id)
	if err != nil {
		return nil, bad("%v", err)
	}
	return map[string]any{"run_ids": ids}, nil
}

func (s *Server) upcoming(w http.ResponseWriter, r *http.Request) (any, error) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 31 {
		days = 7
	}
	now := s.app.Clock()
	runs := s.app.Upcoming(now, now.Add(time.Duration(days)*24*time.Hour), 0)
	if runs == nil {
		runs = []schedule.PlannedRun{}
	}
	return runs, nil
}

// ---- manual control

func (s *Server) runZone(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		ZoneID  int64 `json:"zone_id"`
		Seconds int   `json:"seconds"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	ids, err := s.app.Runner.Enqueue(s.app.Clock(), []runner.Request{{ZoneID: req.ZoneID, Seconds: req.Seconds, Manual: true}})
	if err != nil {
		return nil, err
	}
	return map[string]any{"run_ids": ids}, nil
}

func (s *Server) runOnce(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Zones []core.ProgramZone `json:"zones"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if len(req.Zones) == 0 {
		return nil, bad("pick at least one zone")
	}
	var reqs []runner.Request
	for _, z := range req.Zones {
		reqs = append(reqs, runner.Request{ZoneID: z.ZoneID, Seconds: z.Seconds})
	}
	ids, err := s.app.Runner.Enqueue(s.app.Clock(), reqs)
	if err != nil {
		return nil, err
	}
	return map[string]any{"run_ids": ids}, nil
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		ZoneID int64 `json:"zone_id"`
		RunID  int64 `json:"run_id"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	now := s.app.Clock()
	switch {
	case req.RunID > 0:
		if !s.app.Runner.StopRun(now, req.RunID) {
			return nil, httpError{http.StatusNotFound, "run is not queued or running"}
		}
	case req.ZoneID > 0:
		s.app.Runner.StopZone(now, req.ZoneID)
	default:
		s.app.Runner.StopAll(now)
	}
	return s.app.Runner.Snapshot(now), nil
}

func (s *Server) pause(w http.ResponseWriter, r *http.Request) (any, error) {
	s.app.Runner.Pause(s.app.Clock())
	return nil, nil
}

func (s *Server) resume(w http.ResponseWriter, r *http.Request) (any, error) {
	s.app.Runner.Resume(s.app.Clock())
	return nil, nil
}

func (s *Server) clearFault(w http.ResponseWriter, r *http.Request) (any, error) {
	s.app.Runner.ClearFault(s.app.Clock())
	return nil, nil
}

func (s *Server) rainDelay(w http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		Hours float64 `json:"hours"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if err := s.app.SetRainDelay(req.Hours); err != nil {
		return nil, bad("%v", err)
	}
	return s.status(), nil
}

func (s *Server) simSensor(w http.ResponseWriter, r *http.Request) (any, error) {
	if s.app.SimSensor == nil {
		return nil, httpError{http.StatusConflict, "the sensor is not simulated"}
	}
	var req struct {
		Wet bool `json:"wet"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	s.app.SimSensor.SetWet(req.Wet)
	return nil, nil
}

// ---- history

func parseTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(n, 0)
	}
	return time.Time{}
}

func (s *Server) runs(w http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	f := store.RunFilter{Outcome: q.Get("outcome"), Before: parseTime(q.Get("before")), After: parseTime(q.Get("after"))}
	f.ZoneID, _ = strconv.ParseInt(q.Get("zone"), 10, 64)
	if p := q.Get("program"); p == "manual" {
		f.ProgramID = -1
	} else {
		f.ProgramID, _ = strconv.ParseInt(p, 10, 64)
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	runs, err := s.app.Store.Runs(f)
	if runs == nil {
		runs = []core.Run{}
	}
	return runs, err
}

func (s *Server) weekly(w http.ResponseWriter, r *http.Request) (any, error) {
	weeks, _ := strconv.Atoi(r.URL.Query().Get("weeks"))
	if weeks <= 0 || weeks > 104 {
		weeks = 12
	}
	_, _, _, loc := s.app.Snapshot()
	out, err := s.app.Store.WeeklyMinutes(s.app.Clock().AddDate(0, 0, -7*weeks), loc)
	if out == nil {
		out = []store.WeekZone{}
	}
	return out, err
}

func (s *Server) eventLog(w http.ResponseWriter, r *http.Request) (any, error) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	ev, err := s.app.Store.Events(parseTime(r.URL.Query().Get("before")), limit)
	if ev == nil {
		ev = []core.Event{}
	}
	return ev, err
}

// ---- settings and backup

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) (any, error) {
	return s.app.Store.Settings()
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) (any, error) {
	cur, err := s.app.Store.Settings()
	if err != nil {
		return nil, err
	}
	st := cur
	if err := decode(r, &st); err != nil {
		return nil, err
	}
	st.RainDelayUntil = cur.RainDelayUntil // changed through /api/raindelay only
	if _, err := time.LoadLocation(st.Timezone); err != nil || st.Timezone == "" {
		return nil, bad("unknown time zone %q", st.Timezone)
	}
	switch {
	case st.Lat < -90 || st.Lat > 90 || st.Lon < -180 || st.Lon > 180:
		return nil, bad("latitude/longitude out of range")
	case st.WateringLevelPct < 0 || st.WateringLevelPct > 250:
		return nil, bad("watering level must be 0–250%%")
	case st.StationDelayS < 0 || st.StationDelayS > 600:
		return nil, bad("zone delay must be 0–600 s")
	case st.MasterOnDelayS < 0 || st.MasterOnDelayS > 600 || st.MasterOffDelayS < 0 || st.MasterOffDelayS > 600:
		return nil, bad("master delays must be 0–600 s")
	case st.MaxOpenZones < 1 || st.MaxOpenZones > 16:
		return nil, bad("max open zones must be 1–16")
	case st.DailyCapMinutes < 1 || st.DailyCapMinutes > 1440:
		return nil, bad("daily cap must be 1–1440 min")
	case st.Sensor.Normally != "open" && st.Sensor.Normally != "closed":
		return nil, bad("sensor must be normally open or closed")
	case st.Sensor.Kind != "rain" && st.Sensor.Kind != "soil":
		return nil, bad("sensor kind must be rain or soil")
	case st.Sensor.OnDelayS < 0 || st.Sensor.OffDelayS < 0 || st.Sensor.OnDelayS > 86400 || st.Sensor.OffDelayS > 86400:
		return nil, bad("sensor delays must be 0–86400 s")
	case st.Language != "en" && st.Language != "bg":
		return nil, bad("language must be en or bg")
	}
	if st.MasterZoneID != 0 {
		if _, err := s.app.Store.Zone(st.MasterZoneID); err != nil {
			return nil, bad("master zone does not exist")
		}
	}
	if err := s.app.Store.SaveSettings(st); err != nil {
		return nil, err
	}
	_ = s.app.Store.AddEvent(s.app.Clock(), "settings_changed", nil)
	return st, s.app.Reload()
}

func (s *Server) backup(w http.ResponseWriter, r *http.Request) (any, error) {
	b, err := s.app.Store.Export()
	if err != nil {
		return nil, err
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="sprinkler-backup-%s.json"`, time.Now().Format("2006-01-02")))
	return b, nil
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request) (any, error) {
	var b store.Backup
	if err := decode(r, &b); err != nil {
		return nil, err
	}
	now := s.app.Clock()
	s.app.Runner.StopAll(now)
	if err := s.app.Store.Restore(r.Context(), b); err != nil {
		return nil, bad("%v", err)
	}
	_ = s.app.Store.AddEvent(now, "restore", map[string]any{"zones": len(b.Zones), "programs": len(b.Programs)})
	return nil, s.app.Reload()
}
