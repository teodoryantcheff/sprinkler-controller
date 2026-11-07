# Sprinkler controller

A replacement for OpenSprinkler 2.1.8 on `sprinklerpi`: one Go binary with the scheduler, valve driver, HTTP API, embedded web UI and SQLite. See `SPEC.md` for scope and rules, and `AGENTS.md` before touching the live Pi.

Status: Phase 0 (import) and Phase 1 (core) are done and run locally with the simulated driver. Nothing has been installed on the Pi.

## Run it locally

```sh
# one-time: copy OpenSprinkler's data from the Pi (read-only)
rsync -a pi@192.168.1.20:OpenSprinklerGen2/{nvm.dat,stns.dat,logs} testdata/os/

make web            # build the Svelte UI into internal/api/dist (needs Node)
make import         # testdata/os -> dev.db (replaces zones, programs, history)
make run            # http://127.0.0.1:8080, sim valves + simulated rain sensor
```

On first visit the UI asks you to set an admin password. The Home screen shows the simulated outputs and lets you toggle the simulated rain sensor.

To work on the UI with hot reload, run `make run` in one terminal and `cd web && npm run dev` in another. Vite proxies `/api` to :8080.

## Commands

```
sprinkler serve  -config deploy/dev.toml       # or -listen / -db overrides
sprinkler import -from DIR -db FILE [-force] [-dry-run] [-tz Europe/Sofia]
sprinkler version
```

`import -dry-run` prints the zones, programs, run counts and warnings, so you can compare them against the OpenSprinkler UI (the Phase 0 gate).

## Build and test

```sh
make check          # go vet, go test, svelte-check
make cross          # dist/sprinkler-{armv6,armv7,arm64,amd64}, static, no cgo
```

## Layout

```
cmd/sprinkler/      main: flags, TOML config, wiring, systemd notify/watchdog
internal/core/      domain types and validation
internal/schedule/  pure planner: day/start rules, sunrise/sunset, DST, scaling, timeline
internal/runner/    queue + state machine; the only package that touches outputs
internal/hw/        sim, shiftreg (74HC595), gpiorelay drivers; sensor input
internal/app/       minute planner loop, sensor debouncing, crash recovery, live updates
internal/store/     SQLite schema, migrations, queries
internal/api/       JSON API, SSE (/api/stream), auth, embedded UI
internal/importos/  OpenSprinkler 2.1.8 nvm.dat / stns.dat / logs importer
web/                Svelte 5 + TypeScript UI (≈43 KB gzipped)
deploy/             dev.toml, sprinklerpi.toml (shadow mode), systemd unit
```

`internal/app` is a package the spec's layout doesn't list. It holds the wiring between planner, runner, store and sensor, so that `cmd/sprinkler` stays thin and the wiring can be tested.
