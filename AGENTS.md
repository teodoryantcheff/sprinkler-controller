# AGENTS.md

Guidance for coding agents working in this repo. Read `SPEC.md` first: it is the source of truth for scope, data model, scheduling and safety rules.

## What this is

A modern replacement for OpenSprinkler 2.1.8 on a home irrigation Raspberry Pi (`sprinklerpi`, 192.168.1.20). A single Go binary with an embedded Svelte UI and SQLite, running on ARMv6 (Pi 1) up to ARM64/x86-64. Feature parity with OpenSprinkler, no OpenSprinkler API compatibility, OSPy out of scope.

## Status

- Spec written (2026-11-07).
- Phase 0 (import) and Phase 1 (core) implemented (2026-11-08).
- **Cutover done 2026-11-08, at the user's request, without the 14-day Shadow run.** On `sprinklerpi`:
  - The new controller runs as systemd service `sprinkler` (HTTP :80, `shiftreg` driver, real rain sensor on GPIO 14), with config `/etc/sprinkler/sprinkler.toml` (= `deploy/sprinklerpi.toml`) and DB `/var/lib/sprinkler/sprinkler.db`.
  - OpenSprinkler and OSPy are stopped and disabled at boot (`K01` links), with files untouched.
  - Data backup: `~/opensprinkler-2026-11-08.tgz`.
  - All programs are imported disabled (off-season).
  - Updating the Pi: `make deploy` (`deploy/update.sh`, see `docs/raspberry-pi.md#updating`). It installs over the current binary and config (no `.prev` copies) and refuses while anything is watering or queued (`sprinkler busy`): a restart closes all valves. `make deploy-check` copies to `/tmp` on the Pi and checks without installing. Running either one on the Pi needs the user's go-ahead.
- **Cutover gate closed 2026-11-09** (user confirmed): zone tests in the garden and the rollback test are done.
- Since 2026-11-09: scheduled programs wait for the first NTP sync after start-up (no RTC), and finished runs and events older than 6 months are pruned daily. This includes the imported 2026 OpenSprinkler history; the raw logs stay in the backup tgz and on the Pi.
- Added and deployed 2026-11-09 (PR #10, `f643fc6`; migration 2 set every zone cap to 60, first snapshot taken, `/api/health` ok on `shiftreg`):
  - Week view: next 7 days, with skip and restore per program start (new `skips` table, outcome `skipped_user`).
  - History weekly totals: 6/12/26 weeks, with totals per zone and per week.
  - `GET /api/health` (no login; 503 = can't water).
  - Per-zone cap default 60 min. Migration 2 lowers zones still at 120, unless a program runs them for longer than 60 min.
  - Power-loss hardening:
    - `synchronous=FULL` and a `quick_check` at start-up.
    - Daily checked snapshots in `/var/lib/sprinkler/snapshots` (7 kept), with automatic restore of a damaged DB from the newest good one.
    - Atomic TLS file writes.
    - Settings load key by key.
  - Details are in `docs/raspberry-pi.md#power-loss-and-the-sd-card`. `fsck.repair=yes` and root `noatime` were already set on the Pi (checked 2026-11-09).
- 2026-11-10: built-in local CA removed (code, UI, `/ca.crt`, `[tls] hosts`/`dir`). HTTPS now needs `[tls] cert`/`key`; `deploy/sprinklerpi.toml` has no `[tls]`, so HTTPS is off. No phone had the CA installed. Not deployed yet: the Pi still serves HTTPS :443 from the old CA until the next `make deploy`; then `/var/lib/sprinkler/tls` can be deleted (go-ahead needed). Plan: Let's Encrypt for the public name (port 80 forwarded), not committed to the public repo.
- Phase status: 0, 1 and Cutover are done, and Shadow was skipped and is closed (see the table in `SPEC.md`). Next is Phase 2 (weather). The spec's 72 h soak test was never run.
- Runbook (install, stash, rollback) is in `docs/raspberry-pi.md`; dev server docs are in `docs/development.md`, phone setup in `docs/phone.md`. Remaining open questions are at the end of `SPEC.md`.

## The live controller — handle with care

`sprinklerpi` waters a real garden. It is production hardware.

- SSH: `ssh pi@192.168.1.20` (key auth, passwordless sudo). The key has a passphrase. If you get `Permission denied (publickey)`, the ssh-agent is empty: ask the user to run `! ssh-add` rather than trying other keys or passwords.
- **Read-only by default.** Do not start, stop, enable or disable `sprinkler`, `OpenSprinkler` or `ospy`, edit their data, reboot, or touch GPIO on the Pi without the user's explicit go-ahead for that specific action.
- The new controller now owns the valves on the Pi (`sprinkler` service). Restarting it is safe (it starts all-off), but opening a valve (zone tests, manual runs, enabling programs) needs the user's go-ahead. Do not start OpenSprinkler or OSPy while `sprinkler` runs: that is the rollback, and it starts by stopping `sprinkler`.
- Do not try passwords against the OpenSprinkler API (e.g. the default one). Read its data from files instead.
- The Pi is a Pi 1 (700 MHz, single core, 512 MB): keep remote commands light; don't build on it — cross-compile locally.

## Taking over from OpenSprinkler (Shadow and Cutover)

The step-by-step runbook is in `docs/raspberry-pi.md`: build and copy, install in shadow mode, stash OpenSprinkler and OSPy, switch to `shiftreg`, roll back. Rules for agents:

- Every step that runs on the Pi needs the user's explicit go-ahead for that step: installing the service, stopping or disabling OpenSprinkler or OSPy, unexporting GPIO, switching the driver. Approval for one step doesn't cover the next.
- "Stash" means stop and disable, never delete or edit: `/home/pi/OpenSprinklerGen2/` (binary, `nvm.dat`, `stns.dat`, `logs/`), its init script and OSPy stay untouched, so the rollback just starts them again.
- Before stopping OpenSprinkler, confirm nothing is watering. It may not switch the outputs off on exit, and only the new service's start-up all-off clears them.
- Import into a database on the laptop from a fresh `rsync` copy, then copy the `.db` to the Pi. Don't run the importer on the Pi 1, and never point it at the live directory.
- The runbook's facts were checked read-only on 2026-11-08:
  - Units are `OpenSprinkler` (script `/etc/init.d/OpenSprinkler.sh`) and `ospy` (script `/etc/init.d/ospy`). Both run as root and both start at boot.
  - Disable them with `update-rc.d <script name>`, not `systemctl disable`.
  - GPIO 4, 14, 15, 17, 22 and 27 are exported via sysfs and must be unexported after the stop.
  - `/dev/gpiochip0` is `root:gpio 0660`; systemd is 241.
  - If anything differs when you get there, re-check and fix the runbook.
- Record each completed phase in **Status** above.

## Where the source data lives (on the Pi)

| Path | Contents |
| --- | --- |
| `/home/pi/OpenSprinklerGen2/` | OpenSprinkler source (fw 2.1.9 tree; running binary is 2.1.8 from 2018) |
| `/home/pi/OpenSprinklerGen2/nvm.dat` | Binary settings + programs (8 KB) |
| `/home/pi/OpenSprinklerGen2/stns.dat` | Binary station data |
| `/home/pi/OpenSprinklerGen2/logs/<days-since-epoch>.txt` | Daily run logs, JSON lines: `[pid, sid, duration_s, end_epoch]`; `[0,"wl",pct,epoch]` = water level |
| `/home/pi/OpenSprinklerGen2/defines.h`, `OpenSprinkler.cpp`, `program.cpp` | Reference for the nvm/stns layout and scheduling semantics |

Pull copies for the importer with e.g. `rsync -a pi@192.168.1.20:OpenSprinklerGen2/{nvm.dat,stns.dat,logs} testdata/os/` — copies only, never write back. Keep `testdata/os/` out of git if it contains anything sensitive (nvm.dat holds the hashed device password and location).

A full SD card image backup is at `/home/teo/backups/sprinklerpi-2026-11-07.img.zst` (zstd; restore with `zstd -dc … | sudo dd of=/dev/sdX bs=4M`).

## Hardware facts

- Pin numbering: config and docs use BCM GPIO numbers (= `gpiochip0` line offsets), with the physical header pin in brackets. Never put header pin numbers in the config. Full table in `SPEC.md` under the hardware config.
- Valve board: 74HC595 shift register, BCM GPIO latch 22 (header pin 15), data 27 (13), clock 4 (7), output-enable 17 (11) (OE active-low; high = outputs off). 8 outputs; zones 1–5 and 7 in use.
- Rain sensor: one, on GPIO 14 (header pin 8), normally open, with the internal pull-up (reads 1 = dry). Confirmed by the user and by `nvm.dat` (sensor 1 type = rain, option = NO); the logs contain real `rs` events from June/July 2026. In the running 2.1.8 build, GPIO 23 (header pin 16) is *button 1*, not sensor 2 (sensor 2 is a 2.1.9 feature). RF TX on GPIO 15 (header pin 10) is unused.
- No master valve (option `mas` = 0).
- OSPy also drives the same shift-register pins (BOARD numbering 7/11/13/15 = GPIO 4/17/27/22). Its scheduler is disabled, but the service is running (`ospy`, `python run.py` as root) and holds the pins. The new service must refuse to start if another process owns the lines.

## OpenSprinkler 2.1.8 data quirks (learned during Phase 0)

- The source tree on the Pi is 2.1.9; read the 2.1.8 layout from the git tag: `ssh pi@192.168.1.20 'cd OpenSprinklerGen2 && git archive 218 -- defines.h program.h …' | tar x`. A copy lives in `testdata/os/src/`.
- **Log timestamps are local wall-clock seconds** (`now_tz()`), not UTC; the log file name is the local day number. The importer reads them as Europe/Sofia wall time.
- `nvm.dat` options[1] = 56 means a fixed UTC+2 offset. `use_weather` = 0, so the weather service doesn't update it; this hasn't been checked against summer runs. Worth verifying during Shadow (it's winter now, so +2 is correct for the next months).
- ProgramStruct is 176 bytes (flags, days[2], pad, 4×int16 start times, 72×uint16 durations, 20-byte name). Interval programs store `days[0]` = remainder of the *local* epoch day.
- All 7 programs are currently **disabled** (the user ended the season around 2026-11-03). Names starting with `--` are the user's own convention for old programs.
- The station delay byte 122 decodes to 10 s, which matches the 10 s gaps in the logs.

## Engineering conventions

- Go, latest stable. Pure-Go deps only (no cgo) so `GOARM=6` cross-builds need no C toolchain: `modernc.org/sqlite`, `github.com/warthog618/go-gpiocdev`.
- Build: `GOOS=linux GOARCH=arm GOARM=6 go build -o dist/sprinkler-armv6 ./cmd/sprinkler` (also `arm GOARM=7`, `arm64`, `amd64`).
- `internal/schedule` is a pure function of (programs, settings, time); test it with fixed clocks, `Europe/Sofia` DST transitions and sunrise/sunset at lat 42.32, lon 24.04.
- `internal/runner` is the only package allowed to call the output driver. Every exit path ends in `AllOff()`.
- Default to the `sim` driver in development and tests. `make run` uses `deploy/dev.toml` (sim valves + sim rain sensor).
- The built UI goes to `internal/api/dist` (gitignored except `.gitkeep`); run `make web` before `go build` or the binary serves a "UI not built" page.
- Web UI in `web/` (Svelte + TypeScript), built output embedded via `go:embed`; no CDN assets; keep it under 150 KB gzipped.
- Run `make check` (lint, vet, test, govulncheck, svelte-check) before calling work done.

## Formatting and linting

- Go: `gofumpt` + `goimports` (local prefix `sprinkler`) and `golangci-lint` v2 with the config in `.golangci.yml` (standard set plus gosec, errorlint, revive, bodyclose, sqlclosecheck, rowserrcheck, misspell, unconvert, nolintlint). `govulncheck` for vulnerabilities.
- Web: Prettier (`web/.prettierrc.json`) and ESLint (`web/eslint.config.js`, with typescript-eslint and eslint-plugin-svelte), plus `svelte-check`.
- `make fmt` formats everything in place, and `make lint` checks without changing files. `make tools` installs the Go tools; `cd web && npm ci` installs the web ones.
- **Format and lint before every commit.** The `.githooks/pre-commit` hook runs the same checks on staged Go/web changes. Enable it once per clone with `make hooks`, and don't bypass it (`--no-verify`) unless the user asks.
- Fix the code rather than silencing a linter. When a finding is a deliberate choice, use an inline `//nolint:<linter> // <reason>` (`nolintlint` requires the reason) or `// eslint-disable-next-line <rule> -- <reason>`. Widen `.golangci.yml` only for repo-wide policy.
- Keep the `go` directive in `go.mod` at the latest patch release; govulncheck flags stdlib CVEs fixed in newer patches.

## Commit messages — caveman style

Short, plain words, no filler. Few words, much meaning.

- Subject: `area: what changed`, lower case, imperative, at most about 50 characters, no trailing period. Use areas such as `runner`, `api`, `web`, `store`, `importos`, `deploy`, `docs` or `lint`.
- Drop articles, "this commit", "in order to", and politeness.
- Body is optional. Add one only when the *why* isn't obvious: short fragments, one per line, wrapped at 72 characters.
- Examples:
  - `runner: close valves on panic`
  - `api: reject non-JSON writes`
  - `go: bump to 1.26.10, stdlib CVEs`
