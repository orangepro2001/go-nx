#!/usr/bin/env bash
# Pack the core programs (nx-host, nx-probe and their contracts) and publish them to the
# local release-publish server as the application go-nx-core (see docs/release.md).
#   NOTES="what changed" ./release-core.sh      ALLOW_DIRTY=1 skips the clean-tree check.
# The version is CORE_VERSION; scripts/ship.sh --core bumps it, waits for CI and calls this.
set -euo pipefail
cd "$(dirname "$0")"

VERSION=$(tr -d '[:space:]' < CORE_VERSION)
APP=go-nx-core
PLATFORM=win-x64

command -v relhub >/dev/null || { echo "relhub not found in PATH (installed by release-publish's deploy/install.sh)" >&2; exit 1; }

if [[ -z ${ALLOW_DIRTY:-} && -n $(git status --porcelain) ]]; then
	echo "working tree is not clean: commit first so the published programs match a commit (ALLOW_DIRTY=1 to override)" >&2
	exit 1
fi

go vet ./...
GOOS=windows go vet ./...
go test ./...
scripts/artifacts.sh core build/core
ZIP="build/core/go-nx-core-$VERSION-win-x64.zip"
[[ -f $ZIP ]] || { echo "$ZIP was not built" >&2; exit 1; }

# Register the app the first time only; a failure here (server down, no token) stops the script.
if ! relhub app show -id "$APP" >/dev/null 2>&1; then
	relhub app create -id "$APP" -name "go-nx core" -desc "The headless go-nx host (nx-host), its probe (nx-probe) and their contracts, for programs that embed go-nx"
fi

relhub publish -app "$APP" -platform "$PLATFORM" -file "$ZIP" -version "$VERSION" -notes "${NOTES:-}"
echo "published $APP $PLATFORM v$VERSION"
