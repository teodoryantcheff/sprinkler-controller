package importos

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"sprinkler/internal/schedule"
)

// TestCompareWithLogs checks the planner against OpenSprinkler's actual
// program starts in the copied logs (skipped without testdata/os).
func TestCompareWithLogs(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "os")
	if _, err := os.Stat(filepath.Join(dir, "nvm.dat")); err != nil {
		t.Skip("no testdata/os copy")
	}
	sofia, _ := time.LoadLocation("Europe/Sofia")
	d, err := Parse(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	env := schedule.Env{Loc: sofia, Lat: d.Settings.Lat, Lon: d.Settings.Lon}
	first := map[string]time.Time{}
	for _, r := range d.Runs {
		if r.ProgramID == nil {
			continue
		}
		k := schedule.DateOf(*r.Start, sofia).String() + "/" + string(rune('0'+*r.ProgramID))
		if v, ok := first[k]; !ok || r.Start.Before(v) {
			first[k] = *r.Start
		}
	}
	hist := map[int]int{}
	for _, p := range d.Programs {
		p.Enabled = true
		for k, actual := range first {
			if k[len(k)-1] != byte('0'+p.ID) {
				continue
			}
			day := schedule.DateOf(actual, sofia)
			st := schedule.StartsOn(p, day, env)
			if len(st) == 0 {
				hist[-999]++
				continue
			}
			diff := int(actual.Sub(st[0]).Round(time.Minute).Minutes())
			hist[diff]++
			if testing.Verbose() && (diff < -2 || diff > 2) && day.M >= 10 {
				t.Logf("%s %s planned %s actual %s", day, p.Name, st[0].In(sofia).Format("15:04"), actual.In(sofia).Format("15:04"))
			}
		}
	}
	t.Logf("actual - planned (minutes) histogram: %v", hist)
}
