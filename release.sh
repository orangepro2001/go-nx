#!/usr/bin/env bash
# Build go-nx and publish it to the local release-publish server (see docs/release.md).
# NOTES="what changed" ./release.sh      ALLOW_DIRTY=1 skips the clean-tree check.
set -euo pipefail
cd "$(dirname "$0")"

VERSION=$(tr -d '[:space:]' < VERSION)
APP=go-nx
PLATFORM=win-x64

command -v relhub >/dev/null || { echo "relhub not found in PATH (installed by release-publish's deploy/install.sh)" >&2; exit 1; }

if [[ -z ${ALLOW_DIRTY:-} && -n $(git status --porcelain) ]]; then
	echo "working tree is not clean: commit first so the published exe matches a commit (ALLOW_DIRTY=1 to override)" >&2
	exit 1
fi

./build.sh

# Register the app the first time only; a failure here (server down, no token) stops the script.
if ! relhub app show -id "$APP" >/dev/null 2>&1; then
	relhub app create -id "$APP" -name "go-nx" -desc "Lightweight LAN remote control for Windows"
fi

relhub publish -app "$APP" -platform "$PLATFORM" -file build/go-nx.exe -version "$VERSION" -notes "${NOTES:-}"
echo "published $APP $PLATFORM v$VERSION"
