# AGENTS.md

Guidance for coding agents working in this repo. Read `SPEC.md` first: it is the source of truth for scope, data model, scheduling and safety rules.

## What this is

A modern replacement for OpenSprinkler 2.1.8 on a home irrigation Raspberry Pi (`sprinklerpi`, 192.168.1.20). A single Go binary with an embedded Svelte UI and SQLite, running on ARMv6 (Pi 1) up to ARM64/x86-64. Feature parity with OpenSprinkler, no OpenSprinkler API compatibility, OSPy out of scope.

## Status

- Spec written (2026-11-07).
- **Phase 0 (import) and Phase 1 (core) implemented (2026-11-08)**: they run locally with the `sim` driver; `go vet`, `go test` and `svelte-check` are clean. Nothing is installed on the Pi.
- **Next: review the import against the OpenSprinkler UI (the Phase 0 gate), then do a 72 h soak with sim/LEDs, then Shadow.** Shadow config: `deploy/sprinklerpi.toml` (sim driver, sim sensor, port 8090).
- Decisions from the user: zone names come from the live system (read from `nvm.dat`); no master valve; one rain sensor. Remaining open questions are at the end of `SPEC.md`.

## The live controller — handle with care

`sprinklerpi` waters a real garden. It is production hardware.

- SSH: `ssh pi@192.168.1.20` (key auth, passwordless sudo).
- **Read-only by default.** Do not stop, restart or disable `OpenSprinkler` or `ospy`, edit their data, reboot, or touch GPIO on the Pi without the user's explicit go-ahead for that specific action.
- Never run the new controller on the Pi with a real hardware driver (`shiftreg` / `gpiorelay`) before the Cutover phase. Shadow mode uses `sim` only.
- Do not try passwords against the OpenSprinkler API (e.g. the default one). Read its data from files instead.
- The Pi is a Pi 1 (700 MHz, single core, 512 MB): keep remote commands light; don't build on it — cross-compile locally.

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

- Valve board: 74HC595 shift register, BCM GPIO latch 22, data 27, clock 4, output-enable 17 (OE active-low; high = outputs off). 8 outputs; zones 1–5 and 7 in use.
- Rain sensor: one, on GPIO 14, normally open, with the internal pull-up (reads 1 = dry). Confirmed by the user and by `nvm.dat` (sensor 1 type = rain, option = NO); the logs contain real `rs` events from June/July 2026. In the running 2.1.8 build, GPIO 23 is *button 1*, not sensor 2 (sensor 2 is a 2.1.9 feature). RF TX on 15 is unused.
- No master valve (option `mas` = 0).
- OSPy also drives the same shift-register pins (BOARD numbering 7/11/13/15). Its scheduler is disabled, but it holds the pins — the new service must refuse to start if another process owns the lines.

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
- Run `go vet ./...` and `go test ./...` before calling work done.
