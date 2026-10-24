#!/usr/bin/env bash
# One-click build: test, then cross-compile the static, console-less Windows exe into ./build.
set -euo pipefail
cd "$(dirname "$0")"

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
for cmd in cmd/*/; do
	name=$(basename "$cmd")
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		go build -trimpath -ldflags "-s -w -H=windowsgui" -o "build/$name.exe" "./$cmd"
	echo "built build/$name.exe ($(du -h "build/$name.exe" | cut -f1))"
done
