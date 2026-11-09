package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"sprinkler/internal/app"
	"sprinkler/internal/core"
	"sprinkler/internal/hw"
	"sprinkler/internal/runner"
	"sprinkler/internal/store"
)

func newAppServer(t *testing.T) (*httptest.Server, *app.App, *hw.Sim) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	z := core.Zone{OutputIndex: 0, Name: "Lawn", Enabled: true, GroupID: 1, MaxMinutes: 60}
	if err := st.SaveZone(&z); err != nil {
		t.Fatal(err)
	}
	sim := hw.NewSim(8, log)
	loc, _ := time.LoadLocation("Europe/Sofia")
	r, _ := runner.New(sim, st, loc, log)
	a, err := app.New(st, r, log)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(a, 8, log))
	t.Cleanup(srv.Close)
	return srv, a, sim
}

func getHealth(t *testing.T, url string) (int, Health) {
	t.Helper()
	resp, err := http.Get(url + "/api/health") //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var h Health
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, h
}

func TestHealth(t *testing.T) {
	srv, a, sim := newAppServer(t)

	// No login needed; before the runner's first tick it is not healthy.
	if code, h := getHealth(t, srv.URL); code != 503 || h.Status != "fail" {
		t.Fatalf("before first tick: %d %+v", code, h)
	}

	a.Runner.Tick(time.Now())
	a.PlanTick(time.Now()) // trusts the clock (no sync check configured)
	code, h := getHealth(t, srv.URL)
	if code != 200 || h.Status != "ok" {
		t.Fatalf("healthy: %d %+v", code, h)
	}
	for _, k := range []string{"runner", "db", "outputs", "clock"} {
		if c, ok := h.Checks[k].(map[string]any); !ok || c["ok"] != true {
			t.Errorf("check %s: %v", k, h.Checks[k])
		}
	}

	// Snapshots on but none taken yet: degraded, still 200.
	a.SnapshotDir = t.TempDir()
	if code, h := getHealth(t, srv.URL); code != 200 || h.Status != "degraded" {
		t.Fatalf("no snapshot yet: %d %+v", code, h)
	}
	a.SnapshotDir = ""

	// Output fault: 503.
	sim.Fail = 2 // write and retry fail: fault
	if _, err := a.Runner.Enqueue(time.Now(), []runner.Request{{ZoneID: 1, Seconds: 60, Manual: true}}); err != nil {
		t.Fatal(err)
	}
	a.Runner.Tick(time.Now())
	if code, h := getHealth(t, srv.URL); code != 503 || h.Status != "fail" {
		t.Fatalf("fault: %d %+v", code, h)
	}
	a.Runner.ClearFault(time.Now())
	a.Runner.StopAll(time.Now())

	// A stalled runner loop: 503.
	a.Clock = func() time.Time { return time.Now().Add(time.Minute) }
	if code, _ := getHealth(t, srv.URL); code != 503 {
		t.Fatalf("stale runner: %d", code)
	}
}

func TestSkipEndpoint(t *testing.T) {
	srv, a, _ := newAppServer(t)
	c := loggedIn(t, srv.URL)
	sofia, _ := time.LoadLocation("Europe/Sofia")
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, sofia)
	a.Clock = func() time.Time { return now }
	code, p := call(t, c, "POST", srv.URL+"/api/programs",
		`{"name":"Daily","enabled":true,"day_rule":{"kind":"interval","every":1,"anchor":"2026-01-01"},"start_rule":{"kind":"fixed","times":["06:00"]},"zones":[{"zone_id":1,"seconds":600}]}`)
	if code != 200 {
		t.Fatalf("program: %d %v", code, p)
	}
	at := time.Date(2026, 11, 8, 6, 0, 0, 0, sofia).UTC().Format(time.RFC3339)
	if code, out := call(t, c, "POST", srv.URL+"/api/skip", `{"program_id":1,"planned_start":"`+at+`","skip":true}`); code != 200 {
		t.Fatalf("skip: %d %v", code, out)
	}
	var up []map[string]any
	if code := callJSON(t, c, "GET", srv.URL+"/api/upcoming?days=2", "", &up); code != 200 || len(up) != 2 {
		t.Fatalf("upcoming: %d %v", code, up)
	}
	if up[0]["skip"] != core.OutcomeSkippedUser || up[1]["skip"] != nil {
		t.Errorf("upcoming skips: %v", up)
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/skip", `{"program_id":1,"planned_start":"2026-11-08T04:01:00Z","skip":true}`); code != 400 {
		t.Errorf("skip of a time that is not a start: %d", code)
	}
	if code, _ := call(t, client(t), "POST", srv.URL+"/api/skip", `{"program_id":1,"planned_start":"`+at+`","skip":false}`); code != 401 {
		t.Errorf("skip without login: %d", code)
	}
}
