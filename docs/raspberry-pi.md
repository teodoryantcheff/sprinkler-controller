# Taking over from OpenSprinkler on the Pi

This runbook parks OpenSprinkler and OSPy without deleting anything, and runs this controller on the real valves in their place. You can roll back in a minute. Do it on a dry week or out of season. Every command on the Pi is yours to run, or an agent's only with your explicit go-ahead for that step (see [`AGENTS.md`](../AGENTS.md)).

On 2026-11-08 this was carried out at the user's request without the 14-day shadow run. The service was installed directly with the real-valve config from step 4, then the old controllers were stashed (step 3). The zone tests in step 4 and the rollback test were done on 2026-11-09. For later updates, see [Updating](#updating).

## Pin header

What is wired to the Pi's GPIO header. Pin numbers in brackets are physical header pins; names are BCM GPIO numbers, which are what `sprinkler.toml` uses. Pins 1-26 are the same on the Pi 1's 26-pin header and on 40-pin boards. Pin 1 has the square solder pad.

```
                             +-----------+
                       3V3   |  1     2  |  5V
                     GPIO2   |  3     4  |  5V
                     GPIO3   |  5     6  |  GND
  74HC595 clock ---- GPIO4   |  7     8  |  GPIO14 ---- rain sensor (other wire to GND)
                       GND   |  9    10  |  GPIO15      RF transmitter, unused
  74HC595 OE ------- GPIO17  | 11    12  |  GPIO18
  74HC595 data ----- GPIO27  | 13    14  |  GND
  74HC595 latch ---- GPIO22  | 15    16  |  GPIO23      OpenSprinkler button 1, unused
                       3V3   | 17    18  |  GPIO24
                    GPIO10   | 19    20  |  GND
                     GPIO9   | 21    22  |  GPIO25
                    GPIO11   | 23    24  |  GPIO8
                       GND   | 25    26  |  GPIO7
                             +-----------+
```

- **Valves:** the 74HC595 shift register drives 8 outputs (zones 1-5 and 7 in use). OE is active low: while GPIO17 is high, every output is off whatever the register holds. The driver keeps it high until the first all-off write.
- **Rain sensor:** normally open, between GPIO14 and GND, with the internal pull-up. It reads 1 when dry and 0 when the switch closes. GPIO14 is also the UART TX pin, so the serial console must stay off.
- **No master valve** and no flow sensor.

## 0. Before you start

- A full SD image backup exists: `/home/teo/backups/sprinklerpi-2026-11-07.img.zst`. Take a fresh one if OpenSprinkler's programs have changed since then.
- The SSH key must be loaded on the laptop: `ssh-add` (it asks for the passphrase), then check with `ssh pi@192.168.1.20 true`.
- Make sure nothing is watering: the OpenSprinkler page shows no running zone.

## 1. Build and copy (no effect on the running system)

```sh
make web cross                                   # dist/sprinkler-armv6 for the Pi 1
rsync -a pi@192.168.1.20:OpenSprinklerGen2/{nvm.dat,stns.dat,logs} testdata/os/   # fresh copy
./dist/sprinkler import -from testdata/os -db dist/sprinklerpi.db -force          # import here, not on the Pi 1
./dist/sprinkler import -from testdata/os -dry-run | less                          # compare with the OpenSprinkler UI
scp dist/sprinkler-armv6 dist/sprinklerpi.db deploy/sprinkler.service deploy/sprinklerpi.toml pi@192.168.1.20:/tmp/
```

## 2. Install the service in shadow mode (simulated valves, port 8090)

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

In shadow mode it only plans and logs; OpenSprinkler keeps watering. The Shadow gate in [`SPEC.md`](../SPEC.md) is 14 days of planned runs matching OpenSprinkler's actual runs.

## 3. Stash OpenSprinkler and OSPy (the actual takeover)

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

Free the GPIO lines. The old programs use the sysfs interface. While running, they had GPIO 4, 14, 15, 17, 22 and 27 exported, and exports can stay after the programs stop. Release ours (BCM GPIO numbers, header pins in brackets): latch 22 (15), data 27 (13), clock 4 (7), OE 17 (11), rain sensor 14 (8). GPIO 15 (header pin 10, RF transmitter) isn't used by the new controller and can stay.

```sh
ls /sys/class/gpio/
for p in 4 14 17 22 27; do [ -e /sys/class/gpio/gpio$p ] && echo $p | sudo tee /sys/class/gpio/unexport; done
ls /sys/class/gpio/                         # gpio4/14/17/22/27 gone
```

`/dev/gpiochip0` is `root:gpio`, mode `crw-rw----`, so the `sprinkler` user (in group `gpio`) can use it without root.

The new service refuses to start while any of these lines is still held, so a forgotten export or a running OSPy shows up as a start failure, not as two programs fighting over the valves.

## 4. Switch the new service to the real valves

```sh
sudo systemctl stop sprinkler
sudoedit /etc/sprinkler/sprinkler.toml
#   [outputs]      driver = "shiftreg"
#   [inputs.rain]  sim = false        (pin = 14 = GPIO14 / header pin 8, normally = "open" are already set)
sudo systemctl start sprinkler
journalctl -u sprinkler -f               # expect "outputs ready driver=shiftreg count=8"
```

The service starts with every valve off. Then, in the UI at http://192.168.1.20:

1. On Home, the rain sensor should read dry (wet when you short or trip it).
2. On Zones, use **Test 1 min** on each zone in use (1–5 and 7) and check in the garden that the right valve opens and closes.
3. On Programs, turn on the programs you want. They were imported disabled, as OpenSprinkler had them at the end of the season.

The gate to finish the Cutover phase is one week of correct watering, and the rollback below tested once.

The installed config (`deploy/sprinklerpi.toml`) serves HTTP on port 80, as OpenSprinkler did, and HTTPS on 443 (`[tls] listen`). The unit grants `CAP_NET_BIND_SERVICE`, so the service binds those ports without running as root.

## Updating

From the laptop, with the SSH key loaded (`ssh-add`):

```sh
make deploy-check   # build the UI and armv6 binary, copy to /tmp on the Pi, check; installs nothing
make deploy         # the same checks, then install and restart after a y/N prompt
```

`deploy/update.sh` does this:

1. It copies `dist/sprinkler-armv6`, `deploy/sprinklerpi.toml` and `deploy/sprinkler.service` to `/tmp/sprinkler-update` on the Pi.
2. It runs the new binary there (`version`), which catches a wrong architecture before anything is replaced.
3. It runs `sprinkler busy -config /etc/sprinkler/sprinkler.toml` with the new binary, as the `sprinkler` user. This reads the live database read-only, with no migrations. If any run is watering or queued, the script stops: a restart closes every valve.
4. It shows `diff -u` for the config and unit when they differ from what is installed.
5. After the prompt (skip it with `make deploy YES=1`, needed where there is no terminal), it installs over the current binary, config and unit, with no `.prev` copies. It runs `daemon-reload` if the unit changed and restarts `sprinkler`.
6. It waits for the journal line `msg=listening ... version=<new>`. If that line doesn't appear, it prints the log and fails. The valves stay off while the service is down.

Use another host with `HOST=pi@sprinklerpi.local make deploy`. To roll back a bad update, check out the previous commit and run `make deploy` again.

## Rollback to OpenSprinkler

```sh
sudo systemctl disable --now sprinkler   # stops it: all valves off, GPIO released
sudo update-rc.d OpenSprinkler.sh enable
sudo update-rc.d ospy enable             # it was enabled before the takeover
sudo systemctl start OpenSprinkler ospy
systemctl is-active OpenSprinkler ospy   # both: active
```

OpenSprinkler comes back with its own data as you left it. Changes made in the new UI don't flow back. The last resort is the SD image: `zstd -dc /home/teo/backups/sprinklerpi-2026-11-07.img.zst | sudo dd of=/dev/sdX bs=4M`.
