#!/usr/bin/env bash
# Ship one change: bump VERSION, commit, push, wait for CI to pass on that commit, then
# publish the build to the local release-publish server (see docs/release.md).
#
#   scripts/ship.sh [--bump patch|minor|major] [--dry-run] "what changed, for the release notes"
#   scripts/ship.sh --core [--bump ...] [--dry-run] "..."     the same for the application go-nx-core
#                                                             (nx-host, nx-probe and their contracts;
#                                                             version in CORE_VERSION, release-core.sh)
#
# Run it from a clean tree whose HEAD already holds the change (commit that first).
# Default bump is patch: use minor for a feature or a behaviour change, major later.
# Nothing is published unless CI is green for the version commit, so a published
# build always matches a commit that passed. COMMIT_TRAILERS, if set, is appended to
# the version commit's message (e.g. a Co-Authored-By line).
set -euo pipefail
cd "$(dirname "$0")/.."

APP=go-nx
VFILE=VERSION
RELEASE=./release.sh
LABEL=Release
PLATFORM=win-x64
bump=patch
dry=0
core=0
while [[ ${1:-} == --* ]]; do
	case $1 in
		--bump) bump=${2:?--bump needs patch, minor or major}; shift 2 ;;
		--dry-run) dry=1; shift ;;
		--core) core=1; APP=go-nx-core; VFILE=CORE_VERSION; RELEASE=./release-core.sh; LABEL="Release core"; shift ;;
		*) echo "unknown option $1" >&2; exit 2 ;;
	esac
done
notes=${1:-}
[[ -n $notes ]] || { echo "usage: $0 [--core] [--bump patch|minor|major] [--dry-run] \"release notes\"" >&2; exit 2; }
[[ $bump =~ ^(patch|minor|major)$ ]] || { echo "--bump must be patch, minor or major" >&2; exit 2; }

for tool in git gh relhub go; do
	command -v "$tool" >/dev/null || { echo "$tool not found in PATH" >&2; exit 1; }
done
[[ $(git rev-parse --abbrev-ref HEAD) == main ]] || { echo "not on main" >&2; exit 1; }
[[ -z $(git status --porcelain) ]] || { echo "working tree is not clean: commit the change first" >&2; exit 1; }
git fetch -q origin main
[[ $(git rev-parse HEAD) == $(git rev-parse origin/main) || -z $(git log HEAD..origin/main --oneline) ]] || { echo "origin/main has commits this branch lacks: pull first" >&2; exit 1; }

# The next version, and a check that it is newer than what the server has.
cur=$(tr -d '[:space:]' < "$VFILE")
[[ $cur =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]] || { echo "$VFILE '$cur' is not MAJOR.MINOR.PATCH" >&2; exit 1; }
major=${BASH_REMATCH[1]} minor=${BASH_REMATCH[2]} patch=${BASH_REMATCH[3]}
case $bump in
	patch) patch=$((patch + 1)) ;;
	minor) minor=$((minor + 1)); patch=0 ;;
	major) major=$((major + 1)); minor=0; patch=0 ;;
esac
(( minor < 100 && patch < 100 )) || { echo "minor and patch must stay below 100 (versionCode = major*10000 + minor*100 + patch)" >&2; exit 1; }
next="$major.$minor.$patch"
code=$((major * 10000 + minor * 100 + patch))

# The highest versionCode ever published for this platform, yanked or not: a code is never reused.
# (An application that has never been published, like go-nx-core before its first release, has none.)
latest=""
if relhub app show -id "$APP" >/dev/null 2>&1; then
	latest=$(relhub list -app "$APP" | awk -v p="$PLATFORM" '$1 == p && $4 ~ /^[0-9]+$/ { if ($4 > m) m = $4 } END { print m }')
fi
if [[ -n $latest ]] && (( code <= latest )); then
	echo "version $next (code $code) is not newer than the published one (code $latest); bump further or fix $VFILE" >&2
	exit 1
fi
echo "$APP $cur -> $next (versionCode $code; published latest: ${latest:-none})"
if (( core )); then
	# The same programs under a new number would only be noise to whoever follows the app.
	rc=0; changed=$(scripts/core-changed.sh) || rc=$?
	case $rc in
		0) echo "the core has changed since the last core release:"; echo "$changed" | sed 's/^/  /' ;;
		1) echo "nothing the core is made of has changed since the last core release: not releasing" >&2; exit 1 ;;
		*) echo "could not tell whether the core has changed (scripts/core-changed.sh exit $rc)" >&2; exit 1 ;;
	esac
fi
if (( dry )); then echo "dry run: nothing changed"; exit 0; fi

echo "$next" > "$VFILE"
git add "$VFILE"
msg="$LABEL $next"$'\n\n'"$notes"
[[ -z ${COMMIT_TRAILERS:-} ]] || msg+=$'\n\n'"$COMMIT_TRAILERS"
git commit -q -m "$msg"
git push -q origin main
sha=$(git rev-parse HEAD)
echo "pushed $sha; waiting for CI"

# Wait for the CI run of exactly this commit.
deadline=$((SECONDS + 900))
run=""
while (( SECONDS < deadline )); do
	row=$(gh run list --commit "$sha" --json databaseId,status,conclusion -q '.[0] | "\(.databaseId) \(.status) \(.conclusion)"' 2>/dev/null || true)
	if [[ -n $row && $row != "null"* ]]; then
		read -r run status conclusion <<<"$row"
		if [[ $status == completed ]]; then
			[[ $conclusion == success ]] || { echo "CI $run finished with '$conclusion': not publishing. Fix it, commit, and run this again." >&2; exit 1; }
			break
		fi
	fi
	sleep 10
done
[[ -n $run && ${status:-} == completed ]] || { echo "CI did not finish within 15 minutes: not publishing" >&2; exit 1; }
echo "CI $run passed"

NOTES="$notes" "$RELEASE"

# The server must now offer this version to older builds and call it current for itself.
rc=0; relhub check -app "$APP" -platform "$PLATFORM" -code $((code - 1)) >/dev/null || rc=$?
(( rc == 10 )) || { echo "verification failed: an older build is not offered $next (relhub check exit $rc)" >&2; exit 1; }
rc=0; relhub check -app "$APP" -platform "$PLATFORM" -code "$code" >/dev/null || rc=$?
(( rc == 0 )) || { echo "verification failed: $next is not current (relhub check exit $rc)" >&2; exit 1; }
echo "released $APP $next (versionCode $code)"
if (( ! core )); then
	rc=0; changed=$(scripts/core-changed.sh 2>/dev/null) || rc=$?
	if (( rc == 0 )); then
		echo "note: the core (nx-host, nx-probe) has changed since its last release; scripts/ship.sh --core \"...\" publishes it:"
		echo "$changed" | sed 's/^/  /'
	fi
fi
