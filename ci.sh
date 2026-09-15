#!/usr/bin/env bash
# CI gate for the via module: formatting, vet, build, and the race-enabled
# test suite (which includes the no-&/no-closure guarantee lint and the CSP
# unsafe-eval / dead-dash regression guards). Run from the module root.
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

echo "== gofmt =="
unformatted="$($GO fmt ./... )"
if [ -n "$unformatted" ]; then
	echo "gofmt rewrote files (commit them):"
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

# Real-browser tier (separate module, chromedp). A missing binary skips loudly
# rather than silently: vtbrowser's own t.Skip is invisible in CI output, so an
# unrun tier reads as a pass.
if [ -n "$chrome" ] && ! [ -x "$chrome" ]; then
	echo "ci.sh: --chrome=$chrome is not an executable" >&2
	exit 1
fi
if [ -z "$chrome" ] && [ "$browser" != "off" ]; then
	for c in chromium chromium-browser google-chrome google-chrome-stable /bin/chromium; do
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
