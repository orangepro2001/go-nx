#!/usr/bin/env bash
# Build every program of this repository for Windows into a directory, and write what
# they are: SHA256SUMS and artifacts.json (version, commit, toolchain, flags, sizes,
# hashes). build.sh uses it for its compile step; release.sh publishes only go-nx.exe.
#
#   scripts/artifacts.sh [outdir]          build into outdir (default: build)
#   scripts/artifacts.sh --verify          build twice into temporary directories, each with an empty
#                                          build cache, and check that the results are byte-identical
#                                          (a reproducible build); takes a few minutes
#
# A build is reproducible when the same commit, the same Go toolchain and the same flags
# give the same bytes: -trimpath removes the build machine's paths, CGO is off, and the
# only inputs are the tracked files. The Android library (AAR) is built by
# scripts/build-aar.sh when an Android toolchain is available; it is not part of this.
set -euo pipefail
cd "$(dirname "$0")/.."

# Only the programs listed here are windowed (no console). Every other command is a
# console tool whose output has to stay visible, so it is built without -H=windowsgui.
gui=(go-nx)

VERSION=$(tr -d '[:space:]' < VERSION)
[[ $VERSION =~ ^[0-9]{1,3}\.[0-9]{1,2}\.[0-9]{1,2}$ ]] || { echo "VERSION must be MAJOR.MINOR.PATCH (minor, patch < 100), got '$VERSION'" >&2; exit 1; }

build_into() {
	local out=$1 quiet=${2:-}
	mkdir -p "$out"
	local cmd name flags g
	for cmd in cmd/*/; do
		name=$(basename "$cmd")
		flags="-s -w -X main.version=$VERSION"
		for g in "${gui[@]}"; do
			[[ $g == "$name" ]] && flags="$flags -H=windowsgui"
		done
		CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
			go build -trimpath -ldflags "$flags" -o "$out/$name.exe" "./$cmd"
		[[ -n $quiet ]] || echo "built $out/$name.exe v$VERSION ($(du -h "$out/$name.exe" | cut -f1))"
	done
	(cd "$out" && sha256sum -- *.exe > SHA256SUMS)
	write_manifest "$out"
}

write_manifest() {
	local out=$1 commit dirty=false
	commit=$(git rev-parse HEAD 2>/dev/null || echo unknown)
	[[ -z $(git status --porcelain 2>/dev/null) ]] || dirty=true
	{
		printf '{\n  "version": "%s",\n  "commit": "%s",\n  "dirty": %s,\n' "$VERSION" "$commit" "$dirty"
		printf '  "go": "%s",\n  "target": "windows/amd64",\n' "$(go env GOVERSION)"
		printf '  "flags": "CGO_ENABLED=0 go build -trimpath -ldflags \\"-s -w -X main.version=%s [-H=windowsgui for: %s]\\"",\n' "$VERSION" "${gui[*]}"
		printf '  "files": [\n'
		local first=1 f sum size
		for f in "$out"/*.exe; do
			sum=$(sha256sum "$f" | cut -d' ' -f1)
			size=$(stat -c %s "$f")
			[[ $first == 1 ]] || printf ',\n'
			first=0
			printf '    {"name": "%s", "size": %s, "sha256": "%s"}' "$(basename "$f")" "$size" "$sum"
		done
		printf '\n  ]\n}\n'
	} > "$out/artifacts.json"
}

if [[ ${1:-} == --verify ]]; then
	# Each build gets an empty build cache: results taken from a shared cache would be
	# identical because they are the same files, which proves nothing.
	a=$(mktemp -d); b=$(mktemp -d); ca=$(mktemp -d); cb=$(mktemp -d)
	trap 'rm -rf "$a" "$b" "$ca" "$cb"' EXIT
	GOCACHE=$ca build_into "$a" quiet
	GOCACHE=$cb build_into "$b" quiet
	if diff -u "$a/SHA256SUMS" "$b/SHA256SUMS"; then
		echo "ok   two builds of $(git rev-parse --short HEAD 2>/dev/null || echo this tree) with $(go env GOVERSION) are byte-identical:"
		cat "$a/SHA256SUMS"
	else
		echo "the two builds differ: the build is not reproducible" >&2
		exit 1
	fi
else
	build_into "${1:-build}"
fi
