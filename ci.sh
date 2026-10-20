#!/usr/bin/env bash
# CI and release gate for the repo, run from its root: green means releasable.
# It checks and never rewrites files. Every module gets build, vet,
# staticcheck, a complexity cap, a tidy check, govulncheck and race tests run
# shuffled twice; vtbrowser's tests drive a real browser, which must be there.
# The source lints, API pins and dead-code check live in sourcelint_test.go.
set -euo pipefail

GO="${GO:-go}"

if [ $# -gt 0 ]; then
	echo "usage: ci.sh (no flags; VIA_CHROME names the browser binary, else PATH is searched)" >&2
	exit 2
fi

# Same names as vtbrowser's browserNames, so ci.sh finds the binary vtbrowser
# would run. vtbrowser reads VIA_CHROME before PATH, and so does this.
chrome="${VIA_CHROME:-}"
if [ -z "$chrome" ]; then
	for c in chromium chromium-browser chrome google-chrome google-chrome-stable headless-shell /bin/chromium; do
		if command -v "$c" >/dev/null 2>&1; then chrome="$(command -v "$c")"; break; fi
	done
fi
if [ -z "$chrome" ] || ! [ -x "$chrome" ]; then
	echo "ci.sh: no browser binary: set VIA_CHROME to Chromium or Chrome, or put one on PATH" >&2
	exit 1
fi
export VIA_CHROME="$chrome"

# gofmt, not go fmt: go fmt rewrites in place and stops at nested modules.
echo "== gofmt =="
unformatted="$(gofmt -l .)"
if [ -n "$unformatted" ]; then
	echo "gofmt would reformat (run gofmt -w on them):"
	echo "$unformatted"
	exit 1
fi

# check runs every per-module gate in one module. A branchy func does several
# jobs, so complexity is capped; length is not, since a long func with one
# concern is fine. The complexity tools are declared in root's go.mod, so they
# run from root on the module's files; go list keeps out tests, nested modules
# and dot-dirs, which a bare path would walk into.
check() {
	local dir="$1" tags="$2"
	echo "== $dir: build, vet, staticcheck =="
	( cd "$dir" && $GO build $tags ./... && $GO vet $tags ./... && $GO tool staticcheck $tags ./... )

	echo "== $dir: gocyclo (over 10), gocognit (over 15) =="
	local srcs
	srcs="$(cd "$dir" && $GO list $tags -f '{{range .GoFiles}}{{$.Dir}}/{{.}} {{end}}' ./...)"
	$GO tool gocyclo -over 10 $srcs
	$GO tool gocognit -over 15 $srcs

	echo "== $dir: go mod tidy, govulncheck =="
	( cd "$dir" && $GO mod tidy -diff && $GO run golang.org/x/vuln/cmd/govulncheck@v1.9.0 $tags ./... )

	# Shuffled and run twice, so a test that passes only in one order or only
	# on a fresh process fails here.
	echo "== $dir: go test -race -shuffle=on -count=2 =="
	( cd "$dir" && $GO test -race -shuffle=on -count=2 $tags ./... )
}

check . ""
# Separate module: it pulls chroma, which via's own go.mod must not see.
check internal/site ""
# Separate module: chromedp. Its tests need the browser tag.
check vtbrowser -tags=browser

echo "OK"
