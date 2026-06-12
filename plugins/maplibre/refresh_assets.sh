#!/bin/sh
# Re-vendors the embedded MapLibre GL JS dist files at the pinned
# release. Invoked by `go generate ./plugins/maplibre` (dev-time only;
# builds and tests never run this).
#
# The canonical npm tarball from registry.npmjs.org is used instead of a
# CDN (jsdelivr/unpkg): the registry serves the signed package artifact
# and stays reachable from locked-down dev/CI networks that block public
# CDNs. Keep VERSION in sync with pinnedVersion in plugin.go.
set -eu

VERSION=5.24.0
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

curl -fsSL "https://registry.npmjs.org/maplibre-gl/-/maplibre-gl-$VERSION.tgz" \
    -o "$TMP/pkg.tgz"
tar -xzf "$TMP/pkg.tgz" -C "$TMP"

mkdir -p assets
# dist/maplibre-gl.js is already the minified production build; there is
# no .min.js. The CSP bundle needs its companion worker file.
cp "$TMP/package/dist/maplibre-gl.js" assets/
cp "$TMP/package/dist/maplibre-gl-csp.js" assets/
cp "$TMP/package/dist/maplibre-gl-csp-worker.js" assets/
cp "$TMP/package/dist/maplibre-gl.css" assets/
cp "$TMP/package/dist/LICENSE.txt" assets/LICENSE.txt

echo "maplibre: vendored MapLibre GL JS $VERSION into assets/"
