# HTTP API

The JSON API the web UI uses. It is not compatible with the OpenSprinkler API. The source of truth is `internal/api/api.go`; update this file when a route or a JSON shape changes.

- Base URL: `http://<host>/api/` (and `https://` when TLS is configured).
- All bodies are JSON (UTF-8). Every JSON response carries `Cache-Control: no-store`.
- Times are RFC 3339 in UTC (`2026-11-08T05:30:00Z`). Durations are whole seconds unless the field name says otherwise (`_min`, `_minutes`, `hours`).
- IDs are integers. `output_index` is 0-based (output 1 is `0`).
- This page is built into the binary: `GET /api/docs` serves it as HTML and `GET /api/docs.md` as Markdown, both without login.

## Conventions

### Authentication

Log in with `POST /api/login`. The server sets a session cookie, `sprinkler_session`: HMAC-signed, `HttpOnly`, `SameSite=Strict`, valid for 180 days, and `Secure` when the login came over HTTPS. Send it back on every request.

If the `lan_no_login` setting is on, requests from loopback, private (RFC 1918 / ULA) or link-local addresses are let in without a cookie, as user `lan`. Proxy headers are not trusted. The request must also be addressed to a local name: an IP address, a single-label name such as `sprinklerpi`, or a name ending in `.local`, `.lan`, `.home.arpa`, `.internal` or `.localdomain`. Other `Host` names need a login, so a public DNS name pointed at the controller (DNS rebinding) can't use LAN access from a web page. A LAN caller can't change the password (`/api/password` needs a real session).

Endpoints marked **open** need no login. All others return `401 {"error":"login required"}` without one.

### Writes

Every authenticated request that is not `GET`/`HEAD` must send `Content-Type: application/json`, **even with no body** (e.g. `DELETE`, `/api/pause`). Without it the server answers `415 {"error":"send JSON"}`. Together with the `SameSite=Strict` cookie, this keeps cross-site form posts out.

Browser requests that change something (`POST`, `PUT`, `DELETE`, open endpoints included) are also refused with `403` when `Sec-Fetch-Site` or `Origin` shows they come from another site. Clients that send neither header, such as `curl`, are not affected.

Bodies are limited to 1 MiB. Endpoints that read a body need valid JSON; send `{}` where every field is optional. An empty body gives `400 bad JSON: EOF`.

### Responses and errors

- Success: the documented JSON value, or `{"ok": true}` when there is nothing to return.
- Error: `{"error": "<message>"}`. Messages are short, human-readable English that the UI shows as-is.

| Status | When |
| --- | --- |
| 400 | Bad JSON, validation failure, unknown or disabled zone, bad duration, zone already has a manual run, master valve used as a zone |
| 401 | Not logged in, or wrong name/password at login |
| 403 | `/api/setup` after setup is done; wrong current password; cross-site browser write |
| 404 | Unknown ID, run not queued or running, unknown `/api/...` path |
| 409 | Outputs are in a fault state (clear it first); `/api/sim/sensor` with a real sensor |
| 415 | Write without `Content-Type: application/json` |
| 429 | Too many wrong passwords from one address (login or password change) |
| 500 | Anything else (logged on the server) |

### Example

```sh
H='Content-Type: application/json'
curl -c jar -H "$H" -d '{"name":"admin","password":"secret12"}' http://sprinklerpi/api/login
curl -b jar http://sprinklerpi/api/state
curl -b jar -H "$H" -d '{"zone_id":3,"seconds":60}' http://sprinklerpi/api/run/zone
curl -b jar -H "$H" -d '{}' http://sprinklerpi/api/stop
```

## Endpoint index

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| GET | `/api/health` | open | Health check for uptime monitors |
| GET | `/api/me` | open | Who am I, is setup needed |
| POST | `/api/setup` | open | Create the first user |
| POST | `/api/login` | open | Log in |
| POST | `/api/logout` | open | Log out |
| POST | `/api/password` | session | Change password |
| GET | `/api/state` | yes | Home-screen status |
| GET | `/api/stream` | yes | Status as server-sent events |
| GET | `/api/info` | yes | Version and system info |
| GET | `/api/zones` | yes | List zones |
| POST | `/api/zones` | yes | Create zone |
| PUT | `/api/zones/{id}` | yes | Update zone |
| GET | `/api/programs` | yes | List programs |
| POST | `/api/programs` | yes | Create program |
| PUT | `/api/programs/{id}` | yes | Update program |
| DELETE | `/api/programs/{id}` | yes | Delete program |
| POST | `/api/programs/preview` | yes | Next starts of an unsaved program |
| POST | `/api/programs/{id}/run` | yes | Run a program now |
| GET | `/api/upcoming` | yes | Planned runs for the next days |
| POST | `/api/skip` | yes | Skip or restore one planned program start |
| POST | `/api/run/zone` | yes | Manual run of one zone |
| POST | `/api/run/once` | yes | Ad-hoc run of several zones |
| POST | `/api/stop` | yes | Stop a run, a zone or everything |
| POST | `/api/pause` | yes | Pause the runner |
| POST | `/api/resume` | yes | Resume the runner |
| POST | `/api/fault/clear` | yes | Clear an output fault |
| POST | `/api/raindelay` | yes | Set or clear the rain delay |
| POST | `/api/sim/sensor` | yes | Set the simulated sensor |
| GET | `/api/runs` | yes | Run history |
| GET | `/api/history/weekly` | yes | Minutes watered per week and zone |
| GET | `/api/log` | yes | Event log |
| GET | `/api/settings` | yes | Read settings |
| PUT | `/api/settings` | yes | Change settings |
| GET | `/api/backup` | yes | Download backup |
| POST | `/api/restore` | yes | Restore backup |
| GET | `/api/docs` | open | This reference as HTML |
| GET | `/api/docs.md` | open | This reference as Markdown |
| GET | `/ca.crt` | open | Local CA certificate |

Every other path outside `/api/` serves the embedded web UI. Unknown paths get `index.html` so client-side routes work.

## Auth

### `GET /api/me` (open)

```json
{
  "user": "admin",
  "logged_in": true,
  "setup_required": false,
  "language": "en",
  "device_name": "Sprinkler",
  "https": false,
  "ca_available": true
}
```

`user` is `""` when logged out and `"lan"` for LAN access. `setup_required` is true while there are no users. `ca_available` tells you whether `/ca.crt` exists.

### `POST /api/setup` (open)

Creates the first user and logs in. Allowed only while there are no users (`403 already set up` after that).

```json
{ "name": "admin", "password": "at-least-7-chars" }
```

`name` defaults to `admin`. Passwords need at least 7 characters. Passwords are stored as argon2id hashes. If the password is lost, reset it on the host with `sprinkler passwd`.

### `POST /api/login` (open)

Same body as setup (`name` defaults to `admin`). Sets the session cookie. A wrong name or password returns `401` after a 700 ms delay. After 10 wrong passwords within 15 minutes from one address, that address gets `429` until the 15 minutes have passed.

### `POST /api/logout` (open)

Clears the cookie. Needs no body.

### `POST /api/password`

```json
{ "current": "old password", "new": "new password" }
```

Needs a cookie session, not LAN access. `403` if `current` is wrong, `400` if `new` is shorter than 7 characters. Every other session is logged out; the caller gets a fresh cookie. Wrong `current` passwords count towards the login limit (`429`).

## Status

### `GET /api/health` (open)

For uptime monitors (Uptime Kuma, a cron `curl -f`). No login, no secrets: only pass/fail facts.

```json
{
  "status": "ok",
  "version": "v0.1.0",
  "uptime_s": 86400,
  "checks": {
    "runner":  { "ok": true, "last_tick_s": 0 },
    "db":      { "ok": true },
    "outputs": { "ok": true, "driver": "shiftreg" },
    "clock":   { "ok": true },
    "sensor":  { "ok": true },
    "backup":  { "ok": true, "age_h": 7 }
  }
}
```

| `status` | HTTP | Meaning |
| --- | --- | --- |
| `ok` | 200 | Everything passes |
| `degraded` | 200 | Watering works, but the clock hasn't synced yet, the sensor can't be read, or the daily database snapshot is missing, older than 48 h or failed |
| `fail` | 503 | The controller can't water: the runner loop hasn't ticked for 10 s, the database doesn't answer, or the valve outputs are in fault |

`sensor` appears only when a sensor is enabled, and `backup` only when snapshots are on.

### `GET /api/state`

What the Home screen shows.

```json
{
  "now": "2026-11-08T09:12:03Z",
  "runner": {
    "paused": false,
    "fault": "",
    "master_on": false,
    "outputs": [false, false, true, false, false, false, false, false],
    "driver": "shiftreg",
    "running": [
      { "run_id": 812, "zone_id": 3, "program_id": null, "manual": true, "state": "running",
        "seconds": 60, "remaining": 41, "est_start": "2026-11-08T09:11:44Z", "est_end": "2026-11-08T09:12:44Z" }
    ],
    "queue": []
  },
  "next": [ /* up to 3 PlannedRun, see below */ ],
  "watering_level": 100,
  "watering_source": "manual",
  "rain_delay_until": null,
  "sensor": { "enabled": true, "wet": false, "raw": false, "sim": false, "kind": "rain" },
  "controller_on": true,
  "timezone": "Europe/Sofia",
  "sun": { "sunrise": "2026-11-08T05:01:00Z", "sunset": "2026-11-08T15:05:00Z" }
}
```

- `runner.outputs` has one entry per output (the physical state). `driver` is `sim`, `shiftreg` or `gpiorelay`.
- `runner.fault` is non-empty after a valve output failed. All valves are then closed and new runs are refused (`409`) until `POST /api/fault/clear`.
- Run view `state` is one of `queued`, `waiting` (master valve on-delay) or `running`. `program_id` is `null` for manual runs. `seconds` is the target and `remaining` is what is left.
- `next` lists the next 3 planned runs that will actually water, from the next 8 days.
- `rain_delay_until` is `null` when no rain delay is active.
- `sensor.wet` is the debounced state (after on/off delays) and `raw` is the current input. `error` appears if the sensor can't be read.
- `sun` is absent when the sun doesn't rise or set that day (polar locations).

### `GET /api/stream`

Server-sent events (`text/event-stream`). Each event has the same JSON as `/api/state`:

```
event: state
data: {"now":"…","runner":{…},…}
```

The first event is sent at once, then one after each change (bursts coalesced over about 200 ms) and every 30 s as a heartbeat. Without a login it answers plain-text `401`. Browsers send the cookie automatically with `new EventSource('/api/stream')`.

### `GET /api/info`

```json
{ "version": "v0.1.0", "driver": "shiftreg", "outputs": 8, "hostname": "sprinklerpi",
  "go": "go1.26.10", "arch": "arm", "started": "2026-11-08T08:00:00Z" }
```

## Zones

### Zone object

```json
{
  "id": 3,
  "output_index": 2,
  "name": "Lawn front",
  "enabled": true,
  "group_id": 1,
  "uses_master": false,
  "ignore_rain": false,
  "ignore_sensor": false,
  "max_minutes": 60,
  "sort_order": 3
}
```

| Field | Rules |
| --- | --- |
| `output_index` | 0 … outputs−1, unique across zones |
| `name` | required (trimmed) |
| `group_id` | 0–99. Zones in the same group run one after another; `0` runs independently, in parallel with other lanes (limited by `max_open_zones`) |
| `max_minutes` | 1–720; the hard cap for a single run |
| `ignore_rain` / `ignore_sensor` | water despite the rain delay / a wet sensor |

### `GET /api/zones`

Returns an array of zones.

### `POST /api/zones` / `PUT /api/zones/{id}`

The body is a zone object. `id` in the body is ignored: POST creates and PUT updates `{id}` (`404` if it doesn't exist). Returns the saved zone. Zones can't be deleted through the API; disable them instead.

## Programs

### Program object

```json
{
  "id": 2,
  "name": "Lawn mornings",
  "enabled": true,
  "day_rule": { "kind": "weekdays", "days": ["mon", "wed", "fri"] },
  "start_rule": { "kind": "fixed", "times": ["06:00", "sunset-30"] },
  "season_from": "04-15",
  "season_to": "10-15",
  "weather_scaled": true,
  "sort_order": 0,
  "zones": [ { "zone_id": 3, "seconds": 900 }, { "zone_id": 4, "seconds": 600 } ]
}
```

**`day_rule`**: which local calendar days the program runs.

| `kind` | Fields |
| --- | --- |
| `weekdays` | `days`: one or more of `sun mon tue wed thu fri sat` |
| `interval` | `every`: 1–365 days; `anchor`: `YYYY-MM-DD`, a day the program runs |
| `oddeven` | `parity`: `odd` or `even`; `skip31`: with odd, also skip the 31st and 29 Feb |

`parity` may also be set on `weekdays` and `interval` rules as an extra restriction.

**`start_rule`**: when the program starts on a matching day. A time spec is `HH:MM`, `sunrise`, `sunset`, or one of those with an offset in minutes (`sunrise-30`, `sunset+15`, up to ±720).

| `kind` | Fields |
| --- | --- |
| `fixed` | `times`: 1–4 time specs |
| `repeat` | `first`: time spec; `every_min` ≥ 1; `count`: number of repeats after the first (0–1440) |
| `solar` | `event`: `sunrise` or `sunset`; `offset_min`: −720…720 |

**Other fields**

- `season_from` / `season_to`: `MM-DD`, both or neither (`""` = all year).
- `weather_scaled`: durations are multiplied by the watering level (0–250 %). Scaled runs shorter than 10 s are skipped.
- `zones`: steps in order, each with 1 s – 24 h. The master zone can't be a step.

### `GET /api/programs`

Returns an array of programs.

### `POST /api/programs` / `PUT /api/programs/{id}`

The body is a program object. Returns the saved program. `400` with the validation message on a bad rule, or for an unknown zone.

### `DELETE /api/programs/{id}`

Returns `{"ok": true}`. Needs the JSON content type (no body).

### `POST /api/programs/preview`

The body is a program (it may be unsaved; `name` is optional). Returns the next 5 starts within about 400 days, as if the program were enabled, at today's watering level:

```json
[
  { "time": "2026-11-09T04:00:00Z", "total_seconds": 1500, "runs": [ /* PlannedRun */ ] }
]
```

`total_seconds` counts only runs that are not skipped.

### `POST /api/programs/{id}/run`

Starts the program now, ignoring the rain delay and the sensor (not its schedule). Returns `{"run_ids": [813, 814]}`. `400` if it is already queued, has nothing to run, or doesn't exist.

### `GET /api/upcoming?days=7`

Planned runs for the next `days` days (1–31, default 7), skipped ones included. Returns an array of PlannedRun:

```json
{
  "program_id": 2,
  "zone_id": 3,
  "planned_start": "2026-11-09T04:00:00Z",
  "planned_seconds": 900,
  "seconds": 900,
  "scale_pct": 100,
  "skip": "skipped_rain",
  "est_start": "2026-11-09T04:00:00Z"
}
```

- `planned_start` is the program start and `est_start` is the estimated valve-open time after earlier steps and the zone delay.
- `seconds` is the duration after scaling.
- `skip` is absent for runs that will water. Otherwise it is `skipped_rain`, `skipped_sensor`, `skipped_short` or `skipped_user` (skipped with `/api/skip`).

The Week view groups these by `program_id` + `planned_start` (one program start).

### `POST /api/skip`

Skips one future start of a program, or restores it:

```json
{ "program_id": 2, "planned_start": "2026-11-09T04:00:00Z", "skip": true }
```

`planned_start` must be a start the planner would make (as listed by `/api/upcoming`), in the future; otherwise `400` (`no such planned start`). When a skipped start comes due, nothing waters: its zone runs are logged with outcome `skipped_user` and a `program_skipped` event with `"reason": "user"`. Setting and clearing a skip log `start_skip_set` / `start_skip_cleared`.

## Manual control

All of these open or close real valves.

### `POST /api/run/zone`

```json
{ "zone_id": 3, "seconds": 60 }
```

Queues a manual run (1 s – 12 h). The runner cuts it at the zone's `max_minutes` and the daily cap (outcomes `cut_max`, `cut_daily_cap` event). Returns `{"run_ids": [812]}`. Errors: `400` for an unknown or disabled zone, the master zone, a bad duration, or when the zone already has a manual run; `409` in a fault state.

### `POST /api/run/once`

```json
{ "zones": [ { "zone_id": 3, "seconds": 300 }, { "zone_id": 4, "seconds": 300 } ] }
```

A one-off run of several zones, queued like a program (not marked manual). Returns `{"run_ids": [...]}`. Errors are the same as for `/api/run/zone`, plus `400` for an empty list.

### `POST /api/stop`

```json
{ "run_id": 812 }   // stop one run (404 if it is not queued or running)
{ "zone_id": 3 }    // stop everything for one zone
{}                  // stop all
```

Returns the runner state (the `runner` object from `/api/state`).

### `POST /api/pause`, `POST /api/resume`

Pause closes the open valves and holds the queue; resume continues it. No body needed. Returns `{"ok": true}`.

### `POST /api/fault/clear`

Clears `runner.fault` after an output error, so runs are accepted again. No body needed.

### `POST /api/raindelay`

```json
{ "hours": 24 }
```

0–720 hours from now; `0` clears it. Returns the full status, like `/api/state`.

### `POST /api/sim/sensor`

```json
{ "wet": true }
```

Sets the simulated rain sensor (development config). Returns `409` when the sensor is real.

## History

### `GET /api/runs`

Finished runs, newest first. Query parameters (all optional):

| Param | Meaning |
| --- | --- |
| `zone` | zone ID |
| `program` | program ID, or `manual` for runs without a program |
| `outcome` | one of the outcomes below |
| `after`, `before` | planned start ≥ `after`, < `before`; RFC 3339 or Unix seconds. Unparseable values are ignored |
| `limit` | 1–1000, default 100 |

Page backwards by passing the oldest `planned_start` as `before`.

```json
{
  "id": 812,
  "zone_id": 3,
  "program_id": null,
  "planned_start": "2026-11-08T09:11:44Z",
  "start": "2026-11-08T09:11:44Z",
  "end": "2026-11-08T09:12:44Z",
  "planned_seconds": 60,
  "actual_seconds": 60,
  "scale_pct": 100,
  "litres": null,
  "outcome": "done"
}
```

Outcomes: `done`, `stopped`, `skipped_rain`, `skipped_sensor`, `skipped_short`, `skipped_overlap`, `skipped_user`, `cut_max`, `error`. `start` and `end` are `null` when the valve never opened.

### `GET /api/history/weekly?weeks=12`

Minutes watered per week and zone for the last `weeks` weeks (1–104, default 12), in the controller's time zone. `week` is the date of that week's Monday:

```json
[ { "week": "2026-06-29", "zone_id": 3, "minutes": 87.5 } ]
```

### `GET /api/log`

Events other than runs, newest first. `before` (RFC 3339 or Unix seconds) and `limit` (1–1000, default 100).

```json
{ "id": 40, "time": "2026-11-08T08:00:00Z", "kind": "boot", "detail": { } }
```

`detail` is free-form JSON (it may be `null`). Kinds: `boot`, `clock_jump`, `crash_recovery`, `cut_daily_cap`, `cut_max`, `fault_cleared`, `output_error`, `pause`, `resume`, `stop_all`, `program_error`, `program_overlap`, `program_skipped`, `rain_delay_set`, `rain_delay_ended`, `sensor_wet`, `sensor_dry`, `settings_changed`, `restore`, `start_skip_set`, `start_skip_cleared`, `snapshot_failed`, `db_restored`.

## Settings and backup

### `GET /api/settings`

```json
{
  "device_name": "Sprinkler",
  "timezone": "Europe/Sofia",
  "lat": 42.70,
  "lon": 23.32,
  "station_delay_s": 10,
  "master_zone_id": 0,
  "master_on_delay_s": 0,
  "master_off_delay_s": 0,
  "watering_level_pct": 100,
  "rain_delay_until": null,
  "sensor": { "enabled": true, "kind": "rain", "normally": "open", "on_delay_s": 0, "off_delay_s": 0 },
  "max_open_zones": 2,
  "daily_cap_minutes": 240,
  "lan_no_login": false,
  "language": "en",
  "controller_on": true
}
```

### `PUT /api/settings`

The body is merged onto the current settings, so you can send only the fields you change (`sensor` is merged field by field too). `rain_delay_until` is ignored; use `/api/raindelay`. Returns the saved settings and logs a `settings_changed` event.

| Field | Rules |
| --- | --- |
| `timezone` | IANA name |
| `lat` / `lon` | −90…90 / −180…180 |
| `watering_level_pct` | 0–250 |
| `station_delay_s` | 0–600, gap between zones in a lane |
| `master_zone_id` | 0 (none) or an existing zone |
| `master_on_delay_s`, `master_off_delay_s` | 0–600 |
| `max_open_zones` | 1–16 |
| `daily_cap_minutes` | 1–1440, per zone per day |
| `sensor.kind` | `rain` or `soil` |
| `sensor.normally` | `open` or `closed` |
| `sensor.on_delay_s`, `sensor.off_delay_s` | 0–86400 |
| `language` | `en` or `bg` |
| `controller_on` | `false` stops scheduled programs from starting (manual runs still work) |

### `GET /api/backup`

Downloads `sprinkler-backup-YYYY-MM-DD.json` (sent with `Content-Disposition: attachment`):

```json
{ "version": 1, "exported": "2026-11-08T09:00:00Z", "settings": { }, "zones": [ ], "programs": [ ] }
```

The backup covers settings, zones and programs, but not users, history or events.

### `POST /api/restore`

The body is a backup (`version` must be 1). It is validated first with the same rules as the zone, program and settings endpoints (`400` names the bad item, and nothing is stopped). It then **stops all runs** and replaces all zones and programs and the settings in one transaction. Logs a `restore` event.

## `GET /ca.crt` (open)

When HTTPS uses the built-in local CA, this downloads the CA certificate (`sprinkler-ca.crt`), so phones can install it and trust the HTTPS certificate. Returns `404` otherwise.
