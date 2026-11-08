package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sprinkler/internal/app"
	"sprinkler/internal/core"
	"sprinkler/internal/hw"
	"sprinkler/internal/runner"
	"sprinkler/internal/store"
)

func newServer(t *testing.T) (*httptest.Server, *hw.Sim) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	z := core.Zone{OutputIndex: 0, Name: "Lawn", Enabled: true, GroupID: 1, MaxMinutes: 60}
	st.SaveZone(&z)
	sim := hw.NewSim(8, log)
	loc, _ := time.LoadLocation("Europe/Sofia")
	r, _ := runner.New(sim, st, loc, log)
	a, err := app.New(st, r, log)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(a, 8, log))
	t.Cleanup(srv.Close)
	return srv, sim
}

func client(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func call(t *testing.T, c *http.Client, method, url, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func TestAuthFlowAndManualRun(t *testing.T) {
	srv, sim := newServer(t)
	c := client(t)
	code, me := call(t, c, "GET", srv.URL+"/api/me", "")
	if code != 200 || me["setup_required"] != true {
		t.Fatalf("me: %d %v", code, me)
	}
	if code, _ := call(t, c, "GET", srv.URL+"/api/state", ""); code != 401 {
		t.Fatalf("state without login: %d", code)
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/setup", `{"password":"sixsix"}`); code != 400 {
		t.Fatalf("6-character password accepted: %d", code)
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/setup", `{"password":"seven77"}`); code != 200 {
		t.Fatalf("7-character password refused: %d", code)
	}
	if code, _ := call(t, client(t), "POST", srv.URL+"/api/setup", `{"password":"another one"}`); code != 403 {
		t.Fatalf("second setup must be refused: %d", code)
	}
	if code, _ := call(t, c, "GET", srv.URL+"/api/state", ""); code != 200 {
		t.Fatalf("state after setup: %d", code)
	}
	// Writes must be JSON (CSRF guard).
	req, _ := http.NewRequest("POST", srv.URL+"/api/stop", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "text/plain")
	if resp, _ := c.Do(req); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("non-JSON write: %d", resp.StatusCode)
	}
	if code, out := call(t, c, "POST", srv.URL+"/api/run/zone", `{"zone_id":1,"seconds":60}`); code != 200 {
		t.Fatalf("run zone: %d %v", code, out)
	}
	if !sim.States()[0] {
		t.Fatal("valve 1 should be open")
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/stop", `{}`); code != 200 || sim.States()[0] {
		t.Fatal("stop all failed")
	}
	// Fresh client: login with wrong then right password.
	c2 := client(t)
	if code, _ := call(t, c2, "POST", srv.URL+"/api/login", `{"password":"nope"}`); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	if code, _ := call(t, c2, "POST", srv.URL+"/api/login", `{"password":"seven77"}`); code != 200 {
		t.Fatalf("login: %d", code)
	}
	if code, _ := call(t, c2, "POST", srv.URL+"/api/programs", `{"name":"P","enabled":true,"day_rule":{"kind":"weekdays","days":["mon"]},"start_rule":{"kind":"fixed","times":["25:00"]},"zones":[]}`); code != 400 {
		t.Fatalf("invalid program accepted: %d", code)
	}
	if code, out := call(t, c2, "POST", srv.URL+"/api/programs", `{"name":"P","enabled":true,"day_rule":{"kind":"weekdays","days":["mon"]},"start_rule":{"kind":"solar","event":"sunrise","offset_min":-30},"zones":[{"zone_id":1,"seconds":600}]}`); code != 200 {
		t.Fatalf("create program: %d %v", code, out)
	}
}

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("secret pw")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "secret pw") || CheckPassword(h, "secret px") {
		t.Fatal("argon2id check")
	}
}

func TestCADownload(t *testing.T) {
	srv, _ := newServer(t)
	resp, err := http.Get(srv.URL + "/ca.crt")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("no TLS configured: /ca.crt should be 404, got %d", resp.StatusCode)
	}
}
