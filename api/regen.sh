#!/usr/bin/env bash
# Regenerate the committed API baselines under api/.
#
# Run this when you INTENTIONALLY change the public API. The rewritten
# baseline then shows up in the PR diff, where the Core/experimental
# distinction in docs/stability.md is enforced by review. See the
# "Enforcing the contract" section there.
set -e
set -u
set -o pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Pinned apidiff version — must match the one ci-check.sh installs.
APIDIFF_VERSION="${APIDIFF_VERSION:-v0.0.0-20260603202125-055de637280b}"

if ! command -v apidiff >/dev/null 2>&1; then
  go install "golang.org/x/exp/cmd/apidiff@${APIDIFF_VERSION}"
fi

# Public importable packages whose API is the stability contract.
# Keyed by baseline filename -> import path. internal/, viashowcase,
# the plugins example dirs, and test-only packages are deliberately
# excluded; they are not part of the importable surface.
declare -A PKGS=(
  [via.api]="github.com/go-via/via"
  [h.api]="github.com/go-via/via/h"
  [on.api]="github.com/go-via/via/on"
  [sess.api]="github.com/go-via/via/sess"
  [mw.api]="github.com/go-via/via/mw"
)

for file in "${!PKGS[@]}"; do
  pkg="${PKGS[$file]}"
  echo "Writing api/$file <- $pkg"
  apidiff -w "api/$file" "$pkg"
done

echo "OK: baselines regenerated under api/"
