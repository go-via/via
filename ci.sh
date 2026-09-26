#!/usr/bin/env bash
# CI gate for the repo, run from its root. It checks and never rewrites files:
# gofmt over every module; vet, staticcheck, build and race tests for via and
# the site module (internal/site); vet and staticcheck for vtbrowser; then
# vtbrowser's real-browser tier.
set -euo pipefail

GO="${GO:-go}"

browser=auto   # auto | on | off
chrome=""
usage() {
	cat <<'USAGE'
usage: ci.sh [--browser|--no-browser] [--chrome=PATH]

  --browser       require the real-browser tier; fail if no binary is found
  --no-browser    skip the browser tier
  --chrome=PATH   use PATH as the browser binary (implies --browser)

With no flags the tier runs when a Chromium/Chrome binary is present and skips
loudly when one is not.
USAGE
}
while [ $# -gt 0 ]; do
	case "$1" in
	--browser) browser=on ;;
	--no-browser) browser=off ;;
	--chrome=*) chrome="${1#--chrome=}"; browser=on ;;
	--chrome) shift; chrome="${1:-}"; browser=on ;;
	-h | --help) usage; exit 0 ;;
	*) echo "ci.sh: unknown argument: $1" >&2; usage >&2; exit 2 ;;
	esac
	shift
done

# gofmt, not go fmt: go fmt rewrites in place and stops at nested modules.
echo "== gofmt =="
unformatted="$(gofmt -l .)"
if [ -n "$unformatted" ]; then
	echo "gofmt would reformat (run gofmt -w on them):"
	echo "$unformatted"
	exit 1
fi

echo "== go vet =="
$GO vet ./...

echo "== staticcheck =="
$GO tool staticcheck ./...

echo "== go build =="
$GO build ./...

echo "== go test -race =="
$GO test -race ./...

# Separate module: it pulls chroma, which via's own go.mod must not see.
echo "== site module (internal/site) =="
( cd internal/site && $GO build ./... && $GO vet ./... && $GO tool staticcheck ./... && $GO test -race ./... )

# The tests need a browser; vet and staticcheck don't, so they run regardless.
echo "== vtbrowser module (vet, staticcheck) =="
( cd vtbrowser && $GO vet -tags browser ./... && $GO tool staticcheck -tags browser ./... )

# Real-browser tier (separate module, chromedp). A missing binary skips loudly
# rather than silently: vtbrowser's own t.Skip is invisible in CI output, so an
# unrun tier reads as a pass.
if [ -n "$chrome" ] && ! [ -x "$chrome" ]; then
	echo "ci.sh: --chrome=$chrome is not an executable" >&2
	exit 1
fi
if [ -z "$chrome" ] && [ "$browser" != "off" ]; then
	# Same names as vtbrowser's browserNames, so ci.sh never skips a tier
	# vtbrowser would have run.
	for c in chromium chromium-browser chrome google-chrome google-chrome-stable headless-shell /bin/chromium; do
		if command -v "$c" >/dev/null 2>&1; then chrome="$(command -v "$c")"; break; fi
	done
fi

if [ "$browser" = "off" ]; then
	echo "== browser tier SKIPPED (--no-browser) =="
elif [ -n "$chrome" ]; then
	echo "== browser tier (chromedp, -tags browser) =="
	# vtbrowser reads VIA_CHROME itself, so the flag is plumbed in as env.
	( cd vtbrowser && VIA_CHROME="$chrome" $GO test -race -tags browser ./... )
elif [ "$browser" = "on" ]; then
	echo "ci.sh: --browser given but no Chromium/Chrome binary found" >&2
	exit 1
else
	echo "!! WARNING: browser tier SKIPPED — no Chromium/Chrome binary found." >&2
	echo "!! Pass --chrome=/path/to/chromium, or --no-browser to silence this." >&2
fi

echo "OK"
