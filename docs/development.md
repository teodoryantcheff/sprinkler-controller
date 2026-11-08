# Development

## Dev server

The dev server uses `deploy/dev.toml`: simulated valves, a simulated rain sensor, the database in `dev.db`, and port 8080 on this machine only. It never touches GPIO.

### First-time setup

```sh
# copy OpenSprinkler's data from the Pi (read-only; never write back)
rsync -a pi@192.168.1.20:OpenSprinklerGen2/{nvm.dat,stns.dat,logs} testdata/os/

make web            # build the Svelte UI into internal/api/dist (needs Node)
make import         # testdata/os -> dev.db (replaces zones, programs, history)
```

### Start

```sh
make run            # builds the binary, then serves http://127.0.0.1:8080
```

Open http://127.0.0.1:8080. On the first visit to a fresh database it asks you to set an admin password; after that, log in with it. All imported programs are disabled, as they are in OpenSprinkler; turn one on under Programs to see planned runs. The Home screen has a toggle for the simulated rain sensor.

To open it from a phone on the home network, listen on all addresses instead:

```sh
./dist/sprinkler serve -config deploy/dev.toml -listen 0.0.0.0:8080
# then open http://<laptop-ip>:8080
```

To keep it running in the background with a log file:

```sh
nohup ./dist/sprinkler serve -config deploy/dev.toml > /tmp/sprinkler-dev.log 2>&1 &
tail -f /tmp/sprinkler-dev.log
```

### Stop

- Running in the foreground: press `Ctrl+C`.
- Running in the background: `pkill -x sprinkler` (matches the process name only).

Both send a normal shutdown signal. The log ends with `shutting down: all valves off`. If the port is still busy, check what holds it with `ss -ltnp | grep 8080`.

### Reload after changes

| What changed | What to do |
| --- | --- |
| Go code | Stop the server, then `make run` (it rebuilds). |
| Web UI (`web/`) | `cd web && npm run build`, then stop and `make run`; the UI is embedded in the binary. |
| UI with hot reload | Keep `make run` going and also run `cd web && npm run dev`, then open the Vite address (http://localhost:5173). Vite proxies `/api` to :8080, and UI edits show up without a restart. |
| `deploy/dev.toml` | Stop and start again. It is only read at start-up. |
| Zones, programs, settings in the UI | Nothing to do: they apply immediately. |
| Fresh copy of the Pi's data | Stop the server, `rsync` again (see above), `make import`, start again. The import keeps your login. |
| Start over completely | Stop the server, `rm dev.db*`, `make import`, start again. You'll set a new password. |

## Commands

```
sprinkler serve  -config deploy/dev.toml       # or -listen / -db overrides
sprinkler import -from DIR -db FILE [-force] [-dry-run] [-tz Europe/Sofia]
sprinkler passwd [-config FILE | -db FILE] [-user admin] [-keep-sessions]
sprinkler version
```

### Forgot the password

Set a new one from the command line. It asks twice without echo, or reads one line from a pipe. It needs at least 7 characters and logs out every existing session (`-keep-sessions` to skip that). The running service picks it up at once, with no restart.

```sh
# on the Pi
ssh pi@192.168.1.20
sudo -u sprinkler sprinkler passwd -config /etc/sprinkler/sprinkler.toml

# dev server on the laptop
./dist/sprinkler passwd -db dev.db
```

`import -dry-run` prints the zones, programs, run counts and warnings, so you can compare them against the OpenSprinkler UI (the Phase 0 gate).

## Build and test

```sh
make tools hooks    # once per clone: Go linters + pre-commit hook
make fmt            # gofumpt/goimports + Prettier, in place
make check          # lint, go vet, go test, govulncheck, svelte-check
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
internal/api/       JSON API (docs/API.md), SSE (/api/stream), auth, embedded UI
internal/importos/  OpenSprinkler 2.1.8 nvm.dat / stns.dat / logs importer
web/                Svelte 5 + TypeScript UI (≈43 KB gzipped)
deploy/             dev.toml, sprinklerpi.toml (the Pi), systemd unit
```

`internal/app` is a package the spec's layout doesn't list. It holds the wiring between planner, runner, store and sensor, so that `cmd/sprinkler` stays thin and the wiring can be tested.
