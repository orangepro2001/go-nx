#!/usr/bin/env bash
# One-click build: test, then cross-compile the static, console-less Windows exe into ./build.
set -euo pipefail
cd "$(dirname "$0")"

# The release number lives in VERSION; it is compiled in as main.version and is what
# the self-updater compares against the release server (see docs/release.md).
VERSION=$(tr -d '[:space:]' < VERSION)
[[ $VERSION =~ ^[0-9]{1,3}\.[0-9]{1,2}\.[0-9]{1,2}$ ]] || { echo "VERSION must be MAJOR.MINOR.PATCH (minor, patch < 100), got '$VERSION'" >&2; exit 1; }

# Icon assets (icon.ico, icon.png, app.rc) and the exe icon resource.
go run ./tools/genicon
if command -v x86_64-w64-mingw32-windres >/dev/null; then
	(cd cmd/go-nx && x86_64-w64-mingw32-windres -O coff -i app.rc -o rsrc_windows_amd64.syso)
else
	echo "windres not found: keeping the committed cmd/go-nx/rsrc_windows_amd64.syso"
fi

go vet ./...
GOOS=windows go vet ./...
go test ./...

mkdir -p build
# Only the programs listed here are windowed (no console). Every other command is a
# console tool whose output has to stay visible, so it is built without -H=windowsgui.
gui=(go-nx)
for cmd in cmd/*/; do
	name=$(basename "$cmd")
	flags="-s -w -X main.version=$VERSION"
	for g in "${gui[@]}"; do
		[[ $g == "$name" ]] && flags="$flags -H=windowsgui"
	done
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		go build -trimpath -ldflags "$flags" -o "build/$name.exe" "./$cmd"
	echo "built build/$name.exe v$VERSION ($(du -h "build/$name.exe" | cut -f1))"
done
