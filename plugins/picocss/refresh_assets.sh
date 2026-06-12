#!/bin/sh
# Re-vendors the embedded Pico CSS assets at the pinned release.
# Invoked by `go generate ./plugins/picocss` (dev-time only; builds and
# tests never run this).
#
# The canonical npm tarball from registry.npmjs.org is used instead of a
# CDN (jsdelivr/unpkg): the registry serves the signed package artifact
# and stays reachable from locked-down dev/CI networks that block public
# CDNs. Keep VERSION in sync with pinnedVersion in pico.go.
set -eu

VERSION=2.1.1
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

curl -fsSL "https://registry.npmjs.org/@picocss/pico/-/pico-$VERSION.tgz" \
    -o "$TMP/pkg.tgz"
tar -xzf "$TMP/pkg.tgz" -C "$TMP"

THEMES="amber blue cyan fuchsia green grey indigo jade lime orange pink \
pumpkin purple red sand slate violet yellow zinc"

mkdir -p assets
for theme in $THEMES; do
    cp "$TMP/package/css/pico.$theme.min.css" assets/
    cp "$TMP/package/css/pico.classless.$theme.min.css" assets/
done
cp "$TMP/package/css/pico.colors.min.css" assets/
cp "$TMP/package/LICENSE.md" assets/LICENSE.md

echo "picocss: vendored Pico CSS $VERSION into assets/"
