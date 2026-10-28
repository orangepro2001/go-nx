#!/usr/bin/env bash
# Build every program of this repository for Windows into a directory, and write what
# they are: SHA256SUMS and artifacts.json (version, commit, toolchain, flags, sizes,
# hashes). build.sh uses it for its compile step; release.sh publishes only go-nx.exe and
# release-core.sh only the core zip.
#
#   scripts/artifacts.sh [outdir]          build every program into outdir (default: build)
#   scripts/artifacts.sh core [outdir]     build only the core programs (nx-host, nx-probe) into outdir
#                                          (default: build/core) and pack them with their contracts
#                                          into go-nx-core-<CORE_VERSION>-win-x64.zip
#   scripts/artifacts.sh --verify [core]   build twice into temporary directories, each with an empty
#                                          build cache, and check that the results (and the core zip)
#                                          are byte-identical; takes a few minutes ("core": only the
#                                          core programs and their zip, about half as long)
#
# Versions: go-nx.exe carries VERSION; the core programs carry CORE_VERSION, which moves
# only when something they are made of changes (scripts/core-changed.sh says whether that
# happened since the last core release), so that a change to the interface does not make
# a new core release.
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
# The core: the headless host and the probe that checks it, which together with their
# contracts (docs/nx-host.md, docs/protocol.md) are what another application embeds.
core=(nx-host nx-probe)
core_docs=(docs/nx-host.md docs/protocol.md)

semver='^[0-9]{1,3}\.[0-9]{1,2}\.[0-9]{1,2}$'
VERSION=$(tr -d '[:space:]' < VERSION)
CORE_VERSION=$(tr -d '[:space:]' < CORE_VERSION)
[[ $VERSION =~ $semver ]] || { echo "VERSION must be MAJOR.MINOR.PATCH (minor, patch < 100), got '$VERSION'" >&2; exit 1; }
[[ $CORE_VERSION =~ $semver ]] || { echo "CORE_VERSION must be MAJOR.MINOR.PATCH (minor, patch < 100), got '$CORE_VERSION'" >&2; exit 1; }

is_core() { local c; for c in "${core[@]}"; do [[ $c == "$1" ]] && return 0; done; return 1; }
version_of() { if is_core "$1"; then echo "$CORE_VERSION"; else echo "$VERSION"; fi; }

# build_into outdir quiet [program...]: the programs named (default: every cmd/*).
build_into() {
	local out=$1 quiet=$2
	shift 2
	local names=("$@") name flags g v
	if (( ${#names[@]} == 0 )); then
		for cmd in cmd/*/; do names+=("$(basename "$cmd")"); done
	fi
	mkdir -p "$out"
	for name in "${names[@]}"; do
		v=$(version_of "$name")
		flags="-s -w -X main.version=$v"
		for g in "${gui[@]}"; do
			[[ $g == "$name" ]] && flags="$flags -H=windowsgui"
		done
		CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
			go build -trimpath -ldflags "$flags" -o "$out/$name.exe" "./cmd/$name"
		[[ -n $quiet ]] || echo "built $out/$name.exe v$v ($(du -h "$out/$name.exe" | cut -f1))"
	done
	(cd "$out" && sha256sum -- *.exe > SHA256SUMS)
	write_manifest "$out" "${names[@]}"
}

# write_manifest outdir program...: artifacts.json for the programs named. Its version is
# the core's when every program is a core one, otherwise the program's own is in "files".
write_manifest() {
	local out=$1 commit dirty=false top
	shift
	commit=$(git rev-parse HEAD 2>/dev/null || echo unknown)
	[[ -z $(git status --porcelain 2>/dev/null) ]] || dirty=true
	top=$VERSION
	local all_core=1 n
	for n in "$@"; do is_core "$n" || all_core=0; done
	(( all_core )) && top=$CORE_VERSION
	{
		printf '{\n  "version": "%s",\n  "commit": "%s",\n  "dirty": %s,\n' "$top" "$commit" "$dirty"
		printf '  "go": "%s",\n  "target": "windows/amd64",\n' "$(go env GOVERSION)"
		printf '  "flags": "CGO_ENABLED=0 go build -trimpath -ldflags \\"-s -w -X main.version=<version of the file> [-H=windowsgui for: %s]\\"",\n' "${gui[*]}"
		printf '  "files": [\n'
		local first=1 f sum size
		for n in "$@"; do
			f="$out/$n.exe"
			sum=$(sha256sum "$f" | cut -d' ' -f1)
			size=$(stat -c %s "$f")
			[[ $first == 1 ]] || printf ',\n'
			first=0
			printf '    {"name": "%s", "version": "%s", "size": %s, "sha256": "%s"}' "$n.exe" "$(version_of "$n")" "$size" "$sum"
		done
		printf '\n  ]\n}\n'
	} > "$out/artifacts.json"
}

# pack_core outdir: build the core programs into outdir and pack them with their contracts.
# The archive's bytes depend only on its contents and the commit time (tools/mkzip).
pack_core() {
	local out=$1 quiet=${2:-} zip epoch args=() f
	build_into "$out" "$quiet" "${core[@]}"
	zip="$out/go-nx-core-$CORE_VERSION-win-x64.zip"
	epoch=$(git log -1 --format=%ct 2>/dev/null || echo 0)
	for f in "${core[@]}"; do args+=("$out/$f.exe=$f.exe"); done
	args+=("$out/SHA256SUMS=SHA256SUMS" "$out/artifacts.json=artifacts.json")
	for f in "${core_docs[@]}"; do args+=("$f=$f"); done
	go run ./tools/mkzip -o "$zip" -epoch "$epoch" "${args[@]}"
	(cd "$out" && sha256sum -- "$(basename "$zip")" > ZIPSUM)
	[[ -n $quiet ]] || echo "packed $zip ($(du -h "$zip" | cut -f1)) sha256 $(cut -d' ' -f1 "$out/ZIPSUM")"
}

if [[ ${1:-} == --verify ]]; then
	# Each build gets an empty build cache: results taken from a shared cache would be
	# identical because they are the same files, which proves nothing.
	a=$(mktemp -d); b=$(mktemp -d); ca=$(mktemp -d); cb=$(mktemp -d)
	trap 'rm -rf "$a" "$b" "$ca" "$cb"' EXIT
	if [[ ${2:-} != core ]]; then
		GOCACHE=$ca build_into "$a" quiet
		GOCACHE=$cb build_into "$b" quiet
	else
		mkdir -p "$a" "$b"
		: > "$a/SHA256SUMS"; : > "$b/SHA256SUMS"
	fi
	GOCACHE=$ca pack_core "$a/core" quiet
	GOCACHE=$cb pack_core "$b/core" quiet
	if diff -u "$a/SHA256SUMS" "$b/SHA256SUMS" && diff -u "$a/core/SHA256SUMS" "$b/core/SHA256SUMS" && diff -u "$a/core/ZIPSUM" "$b/core/ZIPSUM"; then
		echo "ok   two builds of $(git rev-parse --short HEAD 2>/dev/null || echo this tree) with $(go env GOVERSION) are byte-identical:"
		cat "$a/SHA256SUMS" "$a/core/SHA256SUMS" "$a/core/ZIPSUM"
	else
		echo "the two builds differ: the build is not reproducible" >&2
		exit 1
	fi
elif [[ ${1:-} == core ]]; then
	pack_core "${2:-build/core}"
else
	build_into "${1:-build}" ""
fi
