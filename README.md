# Sprinkler controller

A home irrigation controller for the Raspberry Pi. It is one Go binary with the scheduler, valve driver, HTTP API, an embedded Svelte web UI and SQLite. It runs on anything from a Pi 1 (ARMv6) to ARM64 and x86-64.

It has been watering a real garden since November 2026.

## Features

- Zones, programs (weekday, interval and odd/even days; fixed, repeating and sunrise/sunset start times), manual runs and run history
- A week view of planned starts (skip or restore one start), and minutes watered per zone per week
- Built for power cuts: synced writes, integrity check at start-up, daily database snapshots with automatic restore, and a `/api/health` endpoint for monitoring
- Rain delay and a rain sensor (normally open or closed) with debouncing
- Valve drivers for a 74HC595 shift-register board, plain GPIO relays, and a simulator for development
- Safety first: only one component switches valves, every exit path closes them all, and the service refuses to start if another process holds the GPIO lines
- A mobile-first web UI (installable as a phone app, under 100 KB gzipped) with live updates, login, and HTTPS from a built-in local certificate authority
- An importer for settings, programs and run history from an existing install

Weather-based adjustment from several providers is planned (Phase 2 in [`SPEC.md`](SPEC.md)).

![Architecture: the planner and HTTP API feed the runner, the only part that switches valves](docs/architecture.png)

*Target design. The weather module is not built yet.*

## Hardware

The reference install is a Raspberry Pi 1 with an OpenSprinkler-style 74HC595 valve board and one rain sensor. Names are BCM GPIO numbers, as `sprinkler.toml` uses them; bracketed numbers are header pins:

```
  74HC595 clock ---- GPIO4   (7) (8)   GPIO14 ---- rain sensor, to GND
  74HC595 OE ------- GPIO17 (11) (12)  GPIO18
  74HC595 data ----- GPIO27 (13) (14)  GND
  74HC595 latch ---- GPIO22 (15) (16)  GPIO23
```

Other pins and boards can be set in the config. Plain GPIO relay boards are supported too. The full header drawing is in [docs/raspberry-pi.md](docs/raspberry-pi.md#pin-header).

## Quick start

Needs Go 1.26+ and Node (for the UI).

```sh
make web            # build the UI into internal/api/dist
make run            # simulated valves and rain sensor, http://127.0.0.1:8080
```

On the first visit, set an admin password. To import existing data, see [docs/development.md](docs/development.md).

Cross-compile for the Pi (static, no cgo):

```sh
make cross          # dist/sprinkler-{armv6,armv7,arm64,amd64}
```

## Documentation

| | |
| --- | --- |
| [SPEC.md](SPEC.md) | Scope, data model, scheduling and safety rules |
| [docs/development.md](docs/development.md) | Dev server, commands, build and test, code layout |
| [docs/raspberry-pi.md](docs/raspberry-pi.md) | Installing and updating on a Raspberry Pi |
| [docs/API.md](docs/API.md) | HTTP API reference, also served by the app at `/api/docs` |
| [docs/phone.md](docs/phone.md) | Trusting the certificate and adding the app to a phone's home screen |
| [AGENTS.md](AGENTS.md) | Notes for coding agents, and the live install's status |
