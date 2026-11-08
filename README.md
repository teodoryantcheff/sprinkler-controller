# Sprinkler controller

A replacement for OpenSprinkler 2.1.8 on `sprinklerpi`: one Go binary with the scheduler, valve driver, HTTP API, embedded web UI and SQLite. See `SPEC.md` for scope and rules, and `AGENTS.md` before touching the live Pi.

Status: Phase 0 (import) and Phase 1 (core) are done. **Since 2026-11-08 it runs on `sprinklerpi` in place of OpenSprinkler**, on the real valve driver, at **http://192.168.1.20** and **https://192.168.1.20** (also `sprinklerpi.local`). OpenSprinkler and OSPy are stopped and disabled, not removed (see the rollback below).

## On a phone

1. **HTTPS (once per phone).** The controller has its own small certificate authority, because no public one issues certificates for a home network. Open http://192.168.1.20/ca.crt (or Settings → App on this phone → Download certificate) and install it:
   - **Android:** Settings → Security → Encryption & credentials → Install a certificate → CA certificate.
   - **iPhone:** Settings → Profile Downloaded → Install, then Settings → General → About → Certificate Trust Settings → turn it on.

   After that, https://192.168.1.20 opens without warnings. Plain http:// keeps working too.
2. **Add to the home screen.**
   - **Android (Chrome):** open the **https** address. An **Install app** button appears on Home and in Settings; the ⋮ menu → Install app also works. Over plain http, Chrome only makes a browser shortcut.
   - **iPhone (Safari):** Share → Add to Home Screen. This works over http or https and opens full-screen like an app.

The certificate authority lives in `/var/lib/sprinkler/tls/` on the Pi (10 years). The server certificate (800 days, the most iOS accepts) is renewed on its own a month before it expires, and re-issued at start-up if the Pi's IP address or name changes. Phones keep trusting it, because the authority stays the same.

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

## Taking over from OpenSprinkler on the Pi

This runbook parks OpenSprinkler and OSPy without deleting anything, and runs this controller on the real valves in their place. You can roll back in a minute. Do it on a dry week or out of season. Every command on the Pi is yours to run, or an agent's only with your explicit go-ahead for that step (see `AGENTS.md`).

On 2026-11-08 this was carried out at the user's request without the 14-day shadow run. The service was installed directly with the real-valve config from step 4, then the old controllers were stashed (step 3). The zone tests in step 4 and a test of the rollback are still to do.

### 0. Before you start

- A full SD image backup exists: `/home/teo/backups/sprinklerpi-2026-11-07.img.zst`. Take a fresh one if OpenSprinkler's programs have changed since then.
- The SSH key must be loaded on the laptop: `ssh-add` (it asks for the passphrase), then check with `ssh pi@192.168.1.20 true`.
- Make sure nothing is watering: the OpenSprinkler page shows no running zone.

### 1. Build and copy (no effect on the running system)

```sh
make web cross                                   # dist/sprinkler-armv6 for the Pi 1
rsync -a pi@192.168.1.20:OpenSprinklerGen2/{nvm.dat,stns.dat,logs} testdata/os/   # fresh copy
./dist/sprinkler import -from testdata/os -db dist/sprinklerpi.db -force          # import here, not on the Pi 1
./dist/sprinkler import -from testdata/os -dry-run | less                          # compare with the OpenSprinkler UI
scp dist/sprinkler-armv6 dist/sprinklerpi.db deploy/sprinkler.service deploy/sprinklerpi.toml pi@192.168.1.20:/tmp/
```

### 2. Install the service in shadow mode (simulated valves, port 8090)

On the Pi:

```sh
sudo install -m 755 /tmp/sprinkler-armv6 /usr/local/bin/sprinkler
sudo useradd --system --no-create-home --shell /usr/sbin/nologin --groups gpio sprinkler
sudo install -d -o sprinkler -g sprinkler /var/lib/sprinkler
sudo install -o sprinkler -g sprinkler -m 640 /tmp/sprinklerpi.db /var/lib/sprinkler/sprinkler.db
sudo install -D -m 644 /tmp/sprinklerpi.toml /etc/sprinkler/sprinkler.toml   # for shadow mode first edit: driver = "sim", rain sim = true, listen = ":8090", no [tls]
sudo install -m 644 /tmp/sprinkler.service /etc/systemd/system/sprinkler.service
sudo systemctl daemon-reload
sudo systemctl enable --now sprinkler
journalctl -u sprinkler -f                       # expect "outputs ready driver=sim" and "listening"
```

Open the UI right away (http://192.168.1.20:8090 with the shadow config; port 80 once the config says so) and set the admin password: the first visitor sets it (at least 7 characters).

In shadow mode it only plans and logs; OpenSprinkler keeps watering. The Shadow gate in `SPEC.md` is 14 days of planned runs matching OpenSprinkler's actual runs.

### 3. Stash OpenSprinkler and OSPy (the actual takeover)

As checked on 2026-11-08, both run as root from SysV init scripts. systemd wraps each in a generated unit, and both start at boot (`S01` links in `/etc/rc2.d`):

| Program | systemd unit | Init script | Process |
| --- | --- | --- | --- |
| OpenSprinkler 2.1.8 | `OpenSprinkler` | `/etc/init.d/OpenSprinkler.sh` | `/home/pi/OpenSprinklerGen2/OpenSprinkler` |
| OSPy (web UI on port 8081) | `ospy` | `/etc/init.d/ospy` | `python run.py` |

OSPy is not idle: it runs, and it holds the same valve pins.

Stop both, then keep them from starting at boot. Stop and start go through `systemctl`. Enable and disable go through `update-rc.d` with the *script* name: the unit is called `OpenSprinkler` but the script is `OpenSprinkler.sh`, so `systemctl disable OpenSprinkler` may not find it.

```sh
sudo systemctl stop OpenSprinkler ospy
sudo update-rc.d OpenSprinkler.sh disable   # keeps the scripts and their links, so it's reversible
sudo update-rc.d ospy disable
systemctl is-active OpenSprinkler ospy      # both: inactive
pgrep -af 'OpenSprinkler|run.py'            # no output
ls /etc/rc2.d | grep -iE 'sprink|ospy'      # now K01… instead of S01…
```

`update-rc.d OpenSprinkler.sh disable` also prints `Failed to disable unit: Unit file OpenSprinkler.sh.service does not exist.` That comes from systemd's compatibility wrapper and is harmless: the `K01` links are what count. Afterwards `systemctl is-enabled OpenSprinkler` reports `generated`, and the unit is not started at boot.

OpenSprinkler may not switch the valves off when it exits. That is why step 0 checks that nothing is watering, and why the new service writes all-off first thing when it starts.

Their binaries, data and logs stay where they are (`/home/pi/OpenSprinklerGen2/` and OSPy's directory); nothing is deleted. Keep an extra copy of OpenSprinkler's data anyway:

```sh
tar czf ~/opensprinkler-$(date +%F).tgz -C /home/pi OpenSprinklerGen2/nvm.dat OpenSprinklerGen2/stns.dat OpenSprinklerGen2/logs
```

Free the GPIO lines. The old programs use the sysfs interface. While running, they had GPIO 4, 14, 15, 17, 22 and 27 exported, and exports can stay after the programs stop. Release ours: latch 22, data 27, clock 4, OE 17, rain sensor 14. GPIO 15 (RF transmitter) isn't used by the new controller and can stay.

```sh
ls /sys/class/gpio/
for p in 4 14 17 22 27; do [ -e /sys/class/gpio/gpio$p ] && echo $p | sudo tee /sys/class/gpio/unexport; done
ls /sys/class/gpio/                         # gpio4/14/17/22/27 gone
```

`/dev/gpiochip0` is `root:gpio`, mode `crw-rw----`, so the `sprinkler` user (in group `gpio`) can use it without root.

The new service refuses to start while any of these lines is still held, so a forgotten export or a running OSPy shows up as a start failure, not as two programs fighting over the valves.

### 4. Switch the new service to the real valves

```sh
sudo systemctl stop sprinkler
sudoedit /etc/sprinkler/sprinkler.toml
#   [outputs]      driver = "shiftreg"
#   [inputs.rain]  sim = false        (pin = 14, normally = "open" are already set)
sudo systemctl start sprinkler
journalctl -u sprinkler -f               # expect "outputs ready driver=shiftreg count=8"
```

The service starts with every valve off. Then, in the UI at http://192.168.1.20:

1. On Home, the rain sensor should read dry (wet when you short or trip it).
2. On Zones, use **Test 1 min** on each zone in use (1–5 and 7) and check in the garden that the right valve opens and closes.
3. On Programs, turn on the programs you want. They were imported disabled, as OpenSprinkler had them at the end of the season.

The gate to finish the Cutover phase is one week of correct watering, and the rollback below tested once.

The installed config (`deploy/sprinklerpi.toml`) serves HTTP on port 80, as OpenSprinkler did, and HTTPS on 443 (`[tls] listen`). The unit grants `CAP_NET_BIND_SERVICE`, so the service binds those ports without running as root.

### Rollback to OpenSprinkler

```sh
sudo systemctl disable --now sprinkler   # stops it: all valves off, GPIO released
sudo update-rc.d OpenSprinkler.sh enable
sudo update-rc.d ospy enable             # it was enabled before the takeover
sudo systemctl start OpenSprinkler ospy
systemctl is-active OpenSprinkler ospy   # both: active
```

OpenSprinkler comes back with its own data as you left it. Changes made in the new UI don't flow back. The last resort is the SD image: `zstd -dc /home/teo/backups/sprinklerpi-2026-11-07.img.zst | sudo dd of=/dev/sdX bs=4M`.

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
