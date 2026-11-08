package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// loggedIn returns a client with an admin session on srv.
func loggedIn(t *testing.T, url string) *http.Client {
	t.Helper()
	c := client(t)
	if code, out := call(t, c, "POST", url+"/api/setup", `{"password":"seven77"}`); code != 200 {
		t.Fatalf("setup: %d %v", code, out)
	}
	return c
}

// callJSON is call for endpoints that return arrays or other non-objects.
func callJSON(t *testing.T, c *http.Client, method, url, body string, out any) int {
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
	b, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode == 200 {
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s %s: %v in %s", method, url, err, b)
		}
	}
	return resp.StatusCode
}

func TestZoneEndpoints(t *testing.T) {
	srv, _ := newServer(t)
	c := loggedIn(t, srv.URL)
	var zones []map[string]any
	if code := callJSON(t, c, "GET", srv.URL+"/api/zones", "", &zones); code != 200 || len(zones) != 1 {
		t.Fatalf("list: %d %v", code, zones)
	}
	code, z := call(t, c, "POST", srv.URL+"/api/zones", `{"name":" Hedge ","output_index":1,"enabled":true,"max_minutes":30}`)
	if code != 200 || z["name"] != "Hedge" || z["id"].(float64) == 0 {
		t.Fatalf("create: %d %v", code, z)
	}
	id := int(z["id"].(float64))
	if code, _ := call(t, c, "POST", srv.URL+"/api/zones", `{"name":"Dup","output_index":1,"max_minutes":30}`); code != 400 {
		t.Errorf("second zone on the same output: %d", code)
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/zones", `{"name":"Far","output_index":8,"max_minutes":30}`); code != 400 {
		t.Errorf("output beyond the driver: %d", code)
	}
	if code, z := call(t, c, "PUT", fmt.Sprintf("%s/api/zones/%d", srv.URL, id), `{"name":"Hedges","output_index":1,"max_minutes":45}`); code != 200 || z["max_minutes"].(float64) != 45 {
		t.Errorf("update: %d %v", code, z)
	}
	if code, _ := call(t, c, "PUT", srv.URL+"/api/zones/999", `{"name":"X","output_index":2,"max_minutes":5}`); code != 404 {
		t.Errorf("update missing zone: %d", code)
	}
	if code, _ := call(t, c, "PUT", srv.URL+"/api/zones/abc", `{"name":"X","output_index":2,"max_minutes":5}`); code != 400 {
		t.Errorf("bad id: %d", code)
	}
}

const morning = `{"name":"Morning","enabled":true,"day_rule":{"kind":"interval","every":1,"anchor":"2026-01-01"},
"start_rule":{"kind":"fixed","times":["06:00"]},"zones":[{"zone_id":1,"seconds":600}]}`

func TestProgramEndpoints(t *testing.T) {
	srv, sim := newServer(t)
	c := loggedIn(t, srv.URL)
	code, p := call(t, c, "POST", srv.URL+"/api/programs", morning)
	if code != 200 {
		t.Fatalf("create: %d %v", code, p)
	}
	id := int(p["id"].(float64))
	var list []map[string]any
	if code := callJSON(t, c, "GET", srv.URL+"/api/programs", "", &list); code != 200 || len(list) != 1 {
		t.Fatalf("list: %d %v", code, list)
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/programs", strings.Replace(morning, `"zone_id":1`, `"zone_id":42`, 1)); code != 400 {
		t.Errorf("unknown zone: %d", code)
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/programs", strings.Replace(morning, `"06:00"`, `"26:00"`, 1)); code != 400 {
		t.Errorf("bad time: %d", code)
	}
	if code, _ := call(t, c, "PUT", fmt.Sprintf("%s/api/programs/%d", srv.URL, id), strings.Replace(morning, "Morning", "Dawn", 1)); code != 200 {
		t.Errorf("update: %d", code)
	}

	var preview []struct {
		Time         time.Time `json:"time"`
		TotalSeconds int       `json:"total_seconds"`
	}
	if code := callJSON(t, c, "POST", srv.URL+"/api/programs/preview", morning, &preview); code != 200 || len(preview) != 5 || preview[0].TotalSeconds != 600 {
		t.Fatalf("preview: %d %+v", code, preview)
	}
	var up []map[string]any
	if code := callJSON(t, c, "GET", srv.URL+"/api/upcoming?days=2", "", &up); code != 200 || len(up) < 1 {
		t.Fatalf("upcoming: %d %v", code, up)
	}

	if code, out := call(t, c, "POST", fmt.Sprintf("%s/api/programs/%d/run", srv.URL, id), "{}"); code != 200 || out["run_ids"] == nil {
		t.Fatalf("run now: %d %v", code, out)
	}
	if code, _ := call(t, c, "POST", fmt.Sprintf("%s/api/programs/%d/run", srv.URL, id), "{}"); code != 400 {
		t.Errorf("running a queued program twice: %d", code)
	}
	waitFor(t, func() bool { return sim.States()[0] }, "program run should open zone 1")
	if code, _ := call(t, c, "POST", srv.URL+"/api/stop", "{}"); code != 200 {
		t.Fatalf("stop all: %d", code)
	}
	waitFor(t, func() bool { return !sim.States()[0] }, "stop all should close zone 1")

	if code, _ := call(t, c, "DELETE", fmt.Sprintf("%s/api/programs/%d", srv.URL, id), ""); code != 415 {
		t.Errorf("delete without JSON content type must be refused: %d", code)
	}
	if code := del(t, c, fmt.Sprintf("%s/api/programs/%d", srv.URL, id)); code != 200 {
		t.Errorf("delete: %d", code)
	}
	if code, _ := call(t, c, "POST", fmt.Sprintf("%s/api/programs/%d/run", srv.URL, id), "{}"); code != 400 {
		t.Errorf("run deleted program: %d", code)
	}
}

// del deletes like the web UI: JSON content type, no body.
func del(t *testing.T, c *http.Client, url string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	for range 50 {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestManualControlEndpoints(t *testing.T) {
	srv, _ := newServer(t)
	c := loggedIn(t, srv.URL)
	if code, _ := call(t, c, "POST", srv.URL+"/api/run/once", `{"zones":[]}`); code != 400 {
		t.Errorf("run once with no zones: %d", code)
	}
	code, out := call(t, c, "POST", srv.URL+"/api/run/once", `{"zones":[{"zone_id":1,"seconds":60}]}`)
	if code != 200 {
		t.Fatalf("run once: %d %v", code, out)
	}
	runID := int(out["run_ids"].([]any)[0].(float64))
	for _, p := range []string{"/api/pause", "/api/resume", "/api/fault/clear"} {
		if code, _ := call(t, c, "POST", srv.URL+p, "{}"); code != 200 {
			t.Errorf("%s: %d", p, code)
		}
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/stop", fmt.Sprintf(`{"run_id":%d}`, runID)); code != 200 {
		t.Errorf("stop run: %d", code)
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/stop", fmt.Sprintf(`{"run_id":%d}`, runID)); code != 404 {
		t.Errorf("stop finished run: %d", code)
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/stop", `{"zone_id":1}`); code != 200 {
		t.Errorf("stop zone: %d", code)
	}

	code, st := call(t, c, "POST", srv.URL+"/api/raindelay", `{"hours":24}`)
	if code != 200 || st["rain_delay_until"] == nil {
		t.Fatalf("rain delay: %d %v", code, st)
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/raindelay", `{"hours":-1}`); code != 400 {
		t.Errorf("negative rain delay: %d", code)
	}
	if code, st := call(t, c, "POST", srv.URL+"/api/raindelay", `{"hours":0}`); code != 200 || st["rain_delay_until"] != nil {
		t.Errorf("clear rain delay: %d %v", code, st)
	}
	// newServer has no simulated sensor.
	if code, _ := call(t, c, "POST", srv.URL+"/api/sim/sensor", `{"wet":true}`); code != 409 {
		t.Errorf("sim sensor on real hardware: %d", code)
	}
}

func TestHistoryAndInfoEndpoints(t *testing.T) {
	srv, _ := newServer(t)
	c := loggedIn(t, srv.URL)
	if code, _ := call(t, c, "POST", srv.URL+"/api/run/zone", `{"zone_id":1,"seconds":60}`); code != 200 {
		t.Fatal("run zone")
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/stop", "{}"); code != 200 {
		t.Fatal("stop")
	}
	var runs []map[string]any
	waitFor(t, func() bool {
		callJSON(t, c, "GET", srv.URL+"/api/runs?program=manual&zone=1&limit=5", "", &runs)
		return len(runs) == 1
	}, "manual run should be in history")
	if runs[0]["outcome"] != "stopped" {
		t.Errorf("outcome %v", runs[0]["outcome"])
	}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	for _, u := range []string{
		"/api/runs?before=" + future, "/api/runs?after=0", "/api/runs?program=1&outcome=done",
		"/api/history/weekly?weeks=4", "/api/log?limit=10", "/api/log?before=" + future,
	} {
		var v []any
		if code := callJSON(t, c, "GET", srv.URL+u, "", &v); code != 200 || v == nil {
			t.Errorf("%s: %d %v", u, code, v)
		}
	}
	var events []map[string]any
	callJSON(t, c, "GET", srv.URL+"/api/log", "", &events)
	if len(events) == 0 {
		t.Error("event log should not be empty after a manual run")
	}
	if code, info := call(t, c, "GET", srv.URL+"/api/info", ""); code != 200 || info["driver"] != "sim" || info["outputs"].(float64) != 8 {
		t.Errorf("info: %d %v", code, info)
	}
}

func TestSettingsEndpoints(t *testing.T) {
	srv, _ := newServer(t)
	c := loggedIn(t, srv.URL)
	code, st := call(t, c, "GET", srv.URL+"/api/settings", "")
	if code != 200 || st["timezone"] != "Europe/Sofia" {
		t.Fatalf("get: %d %v", code, st)
	}
	if code, st := call(t, c, "PUT", srv.URL+"/api/settings", `{"watering_level_pct":80,"rain_delay_until":"2030-01-01T00:00:00Z"}`); code != 200 || st["watering_level_pct"].(float64) != 80 || st["rain_delay_until"] != nil {
		t.Errorf("put (rain delay must not be settable here): %d %v", code, st)
	}
	if code, _ := call(t, c, "PUT", srv.URL+"/api/settings", `{"watering_level_pct":300}`); code != 400 {
		t.Errorf("level 300%%: %d", code)
	}
	if code, _ := call(t, c, "PUT", srv.URL+"/api/settings", `{"master_zone_id":99}`); code != 400 {
		t.Errorf("unknown master zone: %d", code)
	}
	if code, st := call(t, c, "GET", srv.URL+"/api/settings", ""); code != 200 || st["watering_level_pct"].(float64) != 80 {
		t.Errorf("settings not saved: %v", st)
	}
}

func TestBackupRestoreEndpoints(t *testing.T) {
	srv, _ := newServer(t)
	c := loggedIn(t, srv.URL)
	if code, _ := call(t, c, "POST", srv.URL+"/api/programs", morning); code != 200 {
		t.Fatal("create program")
	}
	resp, err := c.Get(srv.URL + "/api/backup")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Disposition"), "sprinkler-backup-") {
		t.Errorf("backup should download as a file: %q", resp.Header.Get("Content-Disposition"))
	}
	if code := del(t, c, srv.URL+"/api/programs/1"); code != 200 {
		t.Fatal("delete program")
	}
	if code, out := call(t, c, "POST", srv.URL+"/api/restore", string(b)); code != 200 {
		t.Fatalf("restore: %d %v", code, out)
	}
	var ps []map[string]any
	if callJSON(t, c, "GET", srv.URL+"/api/programs", "", &ps); len(ps) != 1 {
		t.Fatalf("restore should bring the program back: %v", ps)
	}
	if code, _ := call(t, c, "POST", srv.URL+"/api/restore", `{"version":9}`); code != 400 {
		t.Errorf("bad backup: %d", code)
	}
}

func TestLogoutAndStream(t *testing.T) {
	srv, _ := newServer(t)
	c := loggedIn(t, srv.URL)

	// Unauthenticated stream is refused.
	anon, err := http.Get(srv.URL + "/api/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer anon.Body.Close()
	if anon.StatusCode != 401 {
		t.Fatalf("stream without login: %d", anon.StatusCode)
	}

	resp, err := c.Get(srv.URL + "/api/stream") //nolint:bodyclose // closed by the defer below; read in a goroutine
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data: ") {
				line <- strings.TrimPrefix(sc.Text(), "data: ")
				return
			}
		}
	}()
	select {
	case d := <-line:
		var st map[string]any
		if err := json.Unmarshal([]byte(d), &st); err != nil || st["runner"] == nil {
			t.Fatalf("first event is not a status: %v %s", err, d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event from /api/stream")
	}
	resp.Body.Close()

	if code, _ := call(t, c, "POST", srv.URL+"/api/logout", "{}"); code != 200 {
		t.Fatalf("logout: %d", code)
	}
	if code, _ := call(t, c, "GET", srv.URL+"/api/state", ""); code != 401 {
		t.Errorf("state after logout: %d", code)
	}
}
