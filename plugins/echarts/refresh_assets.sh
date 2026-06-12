#!/bin/sh
# Re-vendors the embedded ECharts dist build at the pinned release.
# Invoked by `go generate ./plugins/echarts` (dev-time only; builds and
# tests never run this).
#
# The canonical npm tarball from registry.npmjs.org is used instead of a
# CDN (jsdelivr/unpkg): the registry serves the signed package artifact
# and stays reachable from locked-down dev/CI networks that block public
# CDNs. Keep VERSION in sync with pinnedVersion in plugin.go.
set -eu

VERSION=6.0.0
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

curl -fsSL "https://registry.npmjs.org/echarts/-/echarts-$VERSION.tgz" \
    -o "$TMP/pkg.tgz"
tar -xzf "$TMP/pkg.tgz" -C "$TMP"

mkdir -p assets
cp "$TMP/package/dist/echarts.min.js" assets/
# Apache-2.0 requires shipping the LICENSE and NOTICE with the artifact.
cp "$TMP/package/LICENSE" assets/LICENSE
cp "$TMP/package/NOTICE" assets/NOTICE

echo "echarts: vendored ECharts $VERSION into assets/"
