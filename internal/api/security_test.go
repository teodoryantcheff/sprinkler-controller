package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCrossOriginWriteRefused(t *testing.T) {
	srv, _ := newServer(t)
	c := client(t)
	if code, _ := call(t, c, "POST", srv.URL+"/api/setup", `{"password":"seven77"}`); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	for _, path := range []string{"/api/stop", "/api/login", "/api/logout"} {
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://evil.example")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("cross-site POST %s: %d, want 403", path, resp.StatusCode)
		}
	}
	// Same-origin requests from the UI still work.
	req, _ := http.NewRequest("POST", srv.URL+"/api/stop", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("same-origin POST: %d", resp.StatusCode)
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv, _ := newServer(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP %q", csp)
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("no X-Frame-Options")
	}
}

func TestLocalHost(t *testing.T) {
	for h, want := range map[string]bool{
		"192.168.1.20":         true,
		"192.168.1.20:80":      true,
		"[fe80::1]:443":        true,
		"sprinklerpi":          true,
		"sprinklerpi.local":    true,
		"SprinklerPi.Local.":   true,
		"pi.home.arpa":         true,
		"pi.lan:8080":          true,
		"localhost:8080":       true,
		"":                     false,
		"evil.example":         false,
		"rebind.attacker.com":  false,
		"sprinklerpi.local.io": false,
	} {
		if got := localHost(h); got != want {
			t.Errorf("localHost(%q) = %v, want %v", h, got, want)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.5:5000"
	r.Host = "rebind.attacker.com"
	if isLAN(r) {
		t.Error("public Host name accepted as LAN")
	}
	r.Host = "192.168.1.20"
	if !isLAN(r) {
		t.Error("LAN request by IP refused")
	}
	r.RemoteAddr = "8.8.8.8:5000"
	if isLAN(r) {
		t.Error("public client accepted as LAN")
	}
}

func TestLoginLimiter(t *testing.T) {
	var l loginLimiter
	r := httptest.NewRequest("POST", "/api/login", nil)
	r.RemoteAddr = "192.168.1.5:5000"
	other := httptest.NewRequest("POST", "/api/login", nil)
	other.RemoteAddr = "192.168.1.6:5000"
	now := time.Now()
	for range maxFailures {
		if l.blocked(r, now) {
			t.Fatal("blocked too early")
		}
		l.fail(r, now)
	}
	if !l.blocked(r, now) {
		t.Fatal("not blocked after max failures")
	}
	if l.blocked(other, now) {
		t.Fatal("another address is blocked")
	}
	if l.blocked(r, now.Add(failWindow+time.Second)) {
		t.Fatal("still blocked after the window")
	}
	l.fail(r, now)
	l.success(r)
	if len(l.fails) != 0 {
		t.Fatalf("success kept failures: %v", l.fails)
	}
}

func TestPasswordChangeLogsOutOtherSessions(t *testing.T) {
	srv, _ := newServer(t)
	a, b := client(t), client(t)
	if code, _ := call(t, a, "POST", srv.URL+"/api/setup", `{"password":"seven77"}`); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	if code, _ := call(t, b, "POST", srv.URL+"/api/login", `{"password":"seven77"}`); code != 200 {
		t.Fatalf("login b: %d", code)
	}
	if code, _ := call(t, a, "POST", srv.URL+"/api/password", `{"current":"seven77","new":"eight888"}`); code != 200 {
		t.Fatalf("password: %d", code)
	}
	if code, _ := call(t, a, "GET", srv.URL+"/api/state", ""); code != 200 {
		t.Errorf("session that changed the password: %d", code)
	}
	if code, _ := call(t, b, "GET", srv.URL+"/api/state", ""); code != 401 {
		t.Errorf("other session after password change: %d, want 401", code)
	}
}

func TestRestoreRejectsUnsafeZoneBeforeStopping(t *testing.T) {
	srv, sim := newServer(t)
	c := client(t)
	if code, _ := call(t, c, "POST", srv.URL+"/api/setup", `{"password":"seven77"}`); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/run/zone", `{"zone_id":1,"seconds":60}`); code != 200 {
		t.Fatalf("run: %d", code)
	}
	backup := `{"version":1,"settings":{"timezone":"Europe/Sofia","max_open_zones":2,"daily_cap_minutes":240,
		"language":"en","sensor":{"kind":"rain","normally":"open"}},
		"zones":[{"id":1,"output_index":0,"name":"Lawn","max_minutes":100000}],"programs":[]}`
	code, body := call(t, c, "POST", srv.URL+"/api/restore", backup)
	if code != 400 || !strings.Contains(body["error"].(string), "max minutes") {
		t.Fatalf("restore with max_minutes 100000: %d %v", code, body)
	}
	if !sim.States()[0] {
		t.Error("a rejected restore stopped the running zone")
	}
}
