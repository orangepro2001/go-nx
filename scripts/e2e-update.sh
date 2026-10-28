#!/usr/bin/env bash
# End-to-end check of the update path against the real relhub binary: a throwaway
# server in a temp dir on a kernel-chosen loopback port, two published builds, then
# the updater test suite pointed at it. Never touches the live release-publish
# service, its data or any tailscale serve path.
#
#   scripts/e2e-update.sh
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
pid=""
cleanup() {
	[ -n "$pid" ] && kill "$pid" 2>/dev/null && wait "$pid" 2>/dev/null || true
	rm -rf "$tmp"
}
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; [ -f "$tmp/server.log" ] && tail -n 20 "$tmp/server.log" >&2; exit 1; }
ok() { echo "ok   $*"; }
command -v relhub >/dev/null || fail "relhub is required in PATH"
command -v curl >/dev/null || fail "curl is required"

export RELHUB_DATA="$tmp/data" # keeps relhub away from the live admin.token
relhub serve -addr 127.0.0.1:0 -prefix /release >"$tmp/server.log" 2>&1 &
pid=$!
port=""
for _ in $(seq 1 100); do
	port="$(sed -n 's/.*msg=listening addr=127.0.0.1:\([0-9]*\).*/\1/p' "$tmp/server.log" | head -n1)"
	[ -n "$port" ] && break
	kill -0 "$pid" 2>/dev/null || fail "server exited during start-up"
	sleep 0.1
done
[ -n "$port" ] || fail "server did not report its port"
export RELHUB_URL="http://127.0.0.1:$port/release"
ok "throwaway server on $RELHUB_URL (data $tmp/data)"

key="$(curl -fsS "$RELHUB_URL/v1/info" | sed -n 's/.*"publicKey":"\([^"]*\)".*/\1/p')"
[ -n "$key" ] || fail "no public key in /v1/info"

head -c 250000 /dev/urandom >"$tmp/go-nx-0.1.0.exe"
head -c 260000 /dev/urandom >"$tmp/go-nx-0.1.1.exe"
relhub app create -id go-nx -name go-nx >/dev/null
relhub publish -app go-nx -platform win-x64 -file "$tmp/go-nx-0.1.0.exe" -version 0.1.0 -notes "first" >/dev/null
relhub publish -app go-nx -platform win-x64 -file "$tmp/go-nx-0.1.1.exe" -version 0.1.1 -notes "second" >/dev/null
ok "published 0.1.0 (code 100) and 0.1.1 (code 101)"

# The CLI's own verdict: 10 = update available, 0 = current.
set +e
relhub check -app go-nx -platform win-x64 -code 100 >/dev/null; [ $? -eq 10 ] || fail "relhub check at code 100 should exit 10"
relhub check -app go-nx -platform win-x64 -code 101 >/dev/null; [ $? -eq 0 ] || fail "relhub check at code 101 should exit 0"
set -e
ok "relhub check exits 10 at code 100 and 0 at code 101"

cd "$root"
RELHUB_E2E_URL="$RELHUB_URL" RELHUB_E2E_KEY="$key" RELHUB_E2E_PAYLOAD="$tmp/go-nx-0.1.1.exe" \
	go test -count=1 -race -v -run 'TestE2E' ./internal/update/ | grep -E '^(=== RUN|--- |\s+--- |ok|FAIL|PASS)' | grep -E -- '--- |^ok|^FAIL'
ok "updater suite passed against the real server"
