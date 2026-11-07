package schedule

import (
	"math"
	"time"
)

// SunTimes returns sunrise and sunset for the local calendar date d at the
// given position, using the NOAA sunrise equation (accurate to about a
// minute at mid latitudes). ok is false during polar day or night.
func SunTimes(d Date, lat, lon float64, loc *time.Location) (rise, set time.Time, ok bool) {
	const rad = math.Pi / 180
	noonUTC := time.Date(d.Y, d.M, d.D, 12, 0, 0, 0, time.UTC)
	jd := float64(noonUTC.Unix())/86400 + 2440587.5
	n := math.Round(jd - 2451545.0 + 0.0008)
	jStar := n - lon/360
	m := math.Mod(357.5291+0.98560028*jStar, 360)
	c := 1.9148*math.Sin(m*rad) + 0.02*math.Sin(2*m*rad) + 0.0003*math.Sin(3*m*rad)
	lambda := math.Mod(m+c+180+102.9372, 360)
	jTransit := 2451545.0 + jStar + 0.0053*math.Sin(m*rad) - 0.0069*math.Sin(2*lambda*rad)
	sinDecl := math.Sin(lambda*rad) * math.Sin(23.4397*rad)
	cosDecl := math.Cos(math.Asin(sinDecl))
	cosW := (math.Sin(-0.833*rad) - math.Sin(lat*rad)*sinDecl) / (math.Cos(lat*rad) * cosDecl)
	if cosW < -1 || cosW > 1 {
		return time.Time{}, time.Time{}, false
	}
	w := math.Acos(cosW) / rad
	toTime := func(j float64) time.Time {
		secs := (j - 2440587.5) * 86400
		return time.Unix(int64(math.Round(secs)), 0).In(loc)
	}
	return toTime(jTransit - w/360), toTime(jTransit + w/360), true
}

// solarMinute returns the local minute-of-day of sunrise or sunset plus an
// offset, clamped to the day like OpenSprinkler does. It falls back to
// 06:00 / 18:00 if the sun does not rise or set.
func solarMinute(d Date, event string, offset int, env Env) int {
	rise, set, ok := SunTimes(d, env.Lat, env.Lon, env.Loc)
	base := 6 * 60
	if event == "sunset" {
		base = 18 * 60
	}
	if ok {
		t := rise
		if event == "sunset" {
			t = set
		}
		base = t.Hour()*60 + t.Minute()
		if t.Second() >= 30 {
			base++
		}
	}
	v := base + offset
	if v < 0 {
		v = 0
	}
	if v > 1439 {
		v = 1439
	}
	return v
}
