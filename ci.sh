#!/usr/bin/env bash
# CI gate for the via/v2 module: formatting, vet, build, and the race-enabled
# test suite (which includes the no-&/no-closure guarantee lint and the CSP
# unsafe-eval / dead-dash regression guards). Run from the module root.
set -euo pipefail

GO="${GO:-go}"

echo "== gofmt =="
unformatted="$($GO fmt ./... )"
if [ -n "$unformatted" ]; then
	echo "gofmt rewrote files (commit them):"
	echo "$unformatted"
	exit 1
fi

echo "== go vet =="
$GO vet ./...

echo "== go build =="
$GO build ./...

echo "== go test -race =="
$GO test -race ./...

# Real-browser tier (separate module, chromedp). Opt-in: it needs a Chromium/
# Chrome binary. VIA_CHROME overrides the path (default /bin/chromium).
if [ "${VIA_BROWSER:-0}" = "1" ]; then
	echo "== browser tier (chromedp, -tags browser) =="
	( cd vtbrowser && $GO test -tags browser ./... )
fi

echo "OK"
