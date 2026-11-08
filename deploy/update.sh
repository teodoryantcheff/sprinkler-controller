#!/usr/bin/env bash
# Update the controller on the Pi with the armv6 build in dist/.
#
#   make deploy                  # build UI + armv6 binary, then run this
#   deploy/update.sh [-n] [-y]   # -n: checks only, change nothing; -y: no prompt
#
# Steps: copy the binary to /tmp on the Pi, run it there (catches a wrong
# architecture), ask it read-only whether anything is watering or queued,
# show config/unit differences, then install over the current files (no
# .prev copies), restart and check that the new version came up.
#
# A restart closes every valve, so the script refuses while anything is
# watering or queued. Override the host with HOST=pi@sprinklerpi.local.
set -euo pipefail

HOST=${HOST:-pi@192.168.1.20}
BIN=${BIN:-dist/sprinkler-armv6}
CONF_SRC=deploy/sprinklerpi.toml
UNIT_SRC=deploy/sprinkler.service
CONF=/etc/sprinkler/sprinkler.toml
UNIT=/etc/systemd/system/sprinkler.service
TMP=/tmp/sprinkler-update

dry=0 yes=0
while getopts ny opt; do
	case $opt in
	n) dry=1 ;;
	y) yes=1 ;;
	*) sed -n '2,13p' "$0"; exit 2 ;;
	esac
done

cd "$(dirname "$0")/.."
say() { printf '\033[1m==> %s\033[0m\n' "$*"; }
die() { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }

[ -x "$BIN" ] || die "$BIN missing: run 'make deploy' (or 'make web cross')"
case $(file -b "$BIN" 2>/dev/null) in
*32-bit*ARM* | "") ;;
*) die "$BIN is not a 32-bit ARM binary" ;;
esac
want=$(git describe --tags --always --dirty 2>/dev/null || echo dev)

# One shared SSH connection for every step: a single handshake (slow on
# the Pi 1) and its warnings shown once, not per command.
ctl=$(mktemp -d)
SSH_OPTS=(-o BatchMode=yes -o ConnectTimeout=8 -o ControlMaster=auto -o "ControlPath=$ctl/%C" -o ControlPersist=120)
cleanup() {
	ssh "${SSH_OPTS[@]}" "$HOST" "rm -rf $TMP" 2>/dev/null || true
	ssh "${SSH_OPTS[@]}" -O exit "$HOST" 2>/dev/null || true
	rm -rf "$ctl"
}
trap cleanup EXIT

say "Connecting to $HOST"
if ! out=$(ssh "${SSH_OPTS[@]}" "$HOST" true 2>&1); then
	case $out in
	*"Permission denied"*) die "ssh key not loaded: run 'ssh-add' and retry" ;;
	*) die "ssh failed: $out" ;;
	esac
fi
run() { ssh "${SSH_OPTS[@]}" "$HOST" "$@"; }

say "Copying $BIN ($(du -h "$BIN" | cut -f1)), config and unit"
run "mkdir -p $TMP"
scp -q "${SSH_OPTS[@]}" "$BIN" "$HOST:$TMP/sprinkler"
scp -q "${SSH_OPTS[@]}" "$CONF_SRC" "$HOST:$TMP/sprinkler.toml"
scp -q "${SSH_OPTS[@]}" "$UNIT_SRC" "$HOST:$TMP/sprinkler.service"
run "chmod 0755 $TMP/sprinkler"

say "Running the new binary on the Pi"
got=$(run "$TMP/sprinkler version") || die "new binary does not run on the Pi"
cur=$(run "/usr/local/bin/sprinkler version 2>/dev/null || echo none")
echo "    installed: $cur"
echo "    new:       $got"
[ "$got" = "$want" ] || echo "    (note: local tree is $want; was dist/ rebuilt?)"

say "Checking that nothing is watering or queued"
# The new binary reads the live DB read-only (no migrations) as the
# service user, so the WAL index is readable.
if ! busy=$(run "sudo -u sprinkler $TMP/sprinkler busy -config $CONF"); then
	echo "    $busy"
	die "refusing to update: a restart would close the valves. Wait or stop the runs in the UI."
fi
echo "    $busy"

changed_conf=0 changed_unit=0
if ! run "cmp -s $TMP/sprinkler.toml $CONF"; then
	changed_conf=1
	say "Config differs ($CONF_SRC -> $CONF):"
	run "diff -u $CONF $TMP/sprinkler.toml" || true
fi
if ! run "cmp -s $TMP/sprinkler.service $UNIT"; then
	changed_unit=1
	say "Unit differs ($UNIT_SRC -> $UNIT):"
	run "diff -u $UNIT $TMP/sprinkler.service" || true
fi

if [ $dry = 1 ]; then
	say "Dry run: nothing installed"
	exit 0
fi
if [ $yes = 0 ]; then
	[ -t 0 ] || die "no terminal for the y/N prompt: run 'make deploy YES=1' (or deploy/update.sh -y)"
	read -r -p "Install $got and restart sprinkler (all valves close)? [y/N] " a
	[ "$a" = y ] || [ "$a" = Y ] || die "aborted"
fi

say "Installing and restarting"
since=$(run "date +%s")
run "sudo install -m 0755 $TMP/sprinkler /usr/local/bin/sprinkler"
[ $changed_conf = 1 ] && run "sudo install -m 0644 $TMP/sprinkler.toml $CONF"
[ $changed_unit = 1 ] && run "sudo install -m 0644 $TMP/sprinkler.service $UNIT && sudo systemctl daemon-reload"
run "sudo systemctl restart sprinkler"

say "Waiting for the service (Pi 1: may take ~20 s)"
for _ in $(seq 1 30); do
	if run "journalctl -u sprinkler --since @$since -o cat | grep -q 'msg=listening .*version=$got'"; then
		run "journalctl -u sprinkler --since @$since -o cat | grep -E 'outputs ready|listening|clock'" | sed 's/^/    /'
		say "Updated to $got"
		exit 0
	fi
	if [ "$(run 'systemctl is-active sprinkler' || true)" = failed ]; then
		break
	fi
	sleep 2
done
run "journalctl -u sprinkler --since @$since -o cat | tail -20" | sed 's/^/    /'
die "service did not come up with $got (see log above). Valves are off while it is down."
