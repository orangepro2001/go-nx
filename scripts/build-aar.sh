#!/usr/bin/env bash
# The Android library (AAR) of the viewer core: gomobile bind of ./mobile.
#
#   scripts/build-aar.sh                 build build/go-nx-core.aar (needs the Android SDK and NDK)
#   scripts/build-aar.sh --check-api     generate the Java bindings and compare their public API with
#                                        mobile/api.golden; needs only gobind, no SDK or NDK
#   scripts/build-aar.sh --update-golden regenerate mobile/api.golden after a deliberate API change
#
# It installs nothing and does not touch this repository's go.mod: golang.org/x/mobile
# is added to a throwaway copy of the tree, which is where the build runs. The
# tools themselves (gomobile, gobind) must already be on PATH; to get them:
#   go install golang.org/x/mobile/cmd/gomobile@$MOBILE_VERSION
#   go install golang.org/x/mobile/cmd/gobind@$MOBILE_VERSION
#
# Environment: MOBILE_VERSION (default below), ANDROID_HOME / ANDROID_NDK_HOME for
# the full build, ANDROID_API (default 24), TARGETS (default android/arm64,android/arm,android/amd64).
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD

MOBILE_VERSION=${MOBILE_VERSION:-v0.0.0-20261011034528-197770444867}
ANDROID_API=${ANDROID_API:-24}
TARGETS=${TARGETS:-android/arm64,android/arm,android/amd64}
mode=build
case ${1:-} in
	"") ;;
	--check-api) mode=check ;;
	--update-golden) mode=golden ;;
	*) echo "usage: $0 [--check-api | --update-golden]" >&2; exit 2 ;;
esac

die() { echo "build-aar: $*" >&2; exit 1; }

command -v go >/dev/null || die "go is not on PATH"
if [[ $mode == build ]]; then
	command -v gomobile >/dev/null || die "gomobile is not on PATH (go install golang.org/x/mobile/cmd/gomobile@$MOBILE_VERSION)"
	[[ -n ${ANDROID_HOME:-} && -d $ANDROID_HOME ]] || die "ANDROID_HOME must point at the Android SDK"
	[[ -n ${ANDROID_NDK_HOME:-} && -d $ANDROID_NDK_HOME ]] || die "ANDROID_NDK_HOME must point at the Android NDK"
else
	command -v gobind >/dev/null || die "gobind is not on PATH (go install golang.org/x/mobile/cmd/gobind@$MOBILE_VERSION)"
fi

# Build in a copy of the working tree (tracked and untracked, not ignored), so that
# adding x/mobile to go.mod does not change the repository.
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git ls-files --cached --others --exclude-standard -z | tar --null -T - -cf - | tar -x -C "$work"
cd "$work"
go mod edit -require="golang.org/x/mobile@$MOBILE_VERSION"
GOFLAGS=-mod=mod go mod download golang.org/x/mobile >/dev/null
GOFLAGS=-mod=mod go mod tidy >/dev/null 2>&1 || GOFLAGS=-mod=mod go mod tidy

# The public API as the Java side sees it: one normalised line per method, constant
# and interface, without the runtime plumbing every binding has.
api() {
	local dir=$1
	grep -hE '^\s*public ' "$dir"/mobile/*.java \
		| sed -E 's/^\s+//; s/\s+/ /g' \
		| grep -vE 'incRefnum|equals\(|hashCode\(|toString\(|void touch\(|class |Proxy|Seq|\{\s*$' \
		| LC_ALL=C sort
}

gen() {
	local out=$1
	mkdir -p "$out"
	GOFLAGS=-mod=mod gobind -lang=go,java -outdir="$out" ./mobile
}

case $mode in
	check|golden)
		gen "$work/gobind"
		api "$work/gobind/java" > "$work/api.txt"
		[[ -s $work/api.txt ]] || die "no public API was generated"
		if [[ $mode == golden ]]; then
			cp "$work/api.txt" "$root/mobile/api.golden"
			echo "updated mobile/api.golden ($(wc -l < "$work/api.txt") lines)"
		elif ! diff -u "$root/mobile/api.golden" "$work/api.txt"; then
			die "the Java API of package mobile changed; if that is intended run $0 --update-golden"
		else
			echo "ok   the Java API of package mobile matches mobile/api.golden ($(wc -l < "$work/api.txt") lines)"
		fi
		;;
	build)
		mkdir -p "$root/build"
		gomobile bind -target="$TARGETS" -androidapi "$ANDROID_API" -javapkg com.orangepro2001.gonx \
			-trimpath -ldflags "-s -w" -o "$root/build/go-nx-core.aar" ./mobile
		echo "built build/go-nx-core.aar ($(du -h "$root/build/go-nx-core.aar" | cut -f1)) from $(git -C "$root" rev-parse --short HEAD)"
		;;
esac
