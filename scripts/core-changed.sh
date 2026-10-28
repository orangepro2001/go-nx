#!/usr/bin/env bash
# Has anything the core programs (nx-host, nx-probe) are made of changed since the last
# core release? Exit 0 and the changed files if so (or if there has been no core release
# yet), exit 1 and nothing if not, exit 2 if it could not tell. scripts/ship.sh --core uses it to refuse a release that
# would be the same programs under a new number; scripts/ship.sh tells you when the
# interface release you just made left the core behind.
#
# "Made of" is: the Go files of every package of this module that the two programs import
# (for windows/amd64, which is what is released), go.mod and go.sum, the contracts packed
# with them, and the build scripts. Test files do not change the programs.
set -euo pipefail
cd "$(dirname "$0")/.."

last=$(git log --grep='^Release core ' -1 --format=%H 2>/dev/null || true)
if [[ -z $last ]]; then
	echo "no core release yet"
	exit 0
fi

root=$(pwd)
files=$(GOOS=windows GOARCH=amd64 go list -deps \
	-f '{{if and (not .Standard) .Module}}{{if .Module.Main}}{{$d := .Dir}}{{range .GoFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}{{range .SysoFiles}}{{$d}}/{{.}}{{"\n"}}{{end}}{{end}}{{end}}' \
	./cmd/nx-host ./cmd/nx-probe) || { echo "core-changed: go list failed" >&2; exit 2; }
files=$(sed "s|^$root/||" <<<"$files" | sort -u)
files+=$'\n'"go.mod"$'\n'"go.sum"$'\n'"docs/nx-host.md"$'\n'"docs/protocol.md"$'\n'"scripts/artifacts.sh"$'\n'"tools/mkzip/main.go"

# shellcheck disable=SC2086
changed=$(git diff --name-only "$last" HEAD -- $files) || { echo "core-changed: git diff failed" >&2; exit 2; }
[[ -n $changed ]] || exit 1
echo "$changed"
