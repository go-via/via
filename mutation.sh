#!/usr/bin/env bash
# PR mutation gate, run from the repo root: mutates the lines changed since
# BASE in the root module and fails when the tests kill fewer of those mutants
# than the floor. The floor is root's efficacy on the first full run
# (2026-10-20); raise it by hand as tests improve, never lower it.
# internal/site (the docs site) and vtbrowser (a test harness) are left out:
# ci.sh tests both, and mutating them bought noise, not confidence.
set -euo pipefail

GO="${GO:-go}"
gremlins="$GO run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0"
floor=85

if [ $# -ne 1 ]; then
	echo "usage: mutation.sh BASE (the git ref the PR merges into, e.g. origin/main)" >&2
	exit 2
fi
out="$(mktemp -d /tmp/mutation.XXXXXX)"
trap 'rm -rf "$out"' EXIT
json="$out/root.json"

# gremlins v0.6.0 counts a mutant that fails to build as killed. Its arithmetic
# mutator turns string + into -, which never builds, so it is off. Its
# --threshold-efficacy flag is ignored, so the score is read from the JSON.
# --integration runs the whole module's tests per mutant; without it a package
# counts only its own tests. The timeout is a multiple of the coverage run, so
# -count=1 keeps that run out of the test cache, where it takes milliseconds
# and every mutant then times out.
export GOFLAGS="-count=1"
$gremlins unleash --integration --coverpkg ./... --timeout-coefficient 30 --arithmetic-base=false \
	--diff "$1" -E '^(internal/site|vtbrowser|\.research)/' -o "$json" .

if ! [ -s "$json" ]; then
	echo "mutation.sh: gremlins wrote no report" >&2
	exit 1
fi
count() { jq --arg s "$1" '[.files[]?.mutations[] | select(.status == $s)] | length' "$json"; }
killed="$(count KILLED)"
lived="$(count LIVED)"
timed="$(count "TIMED OUT")"
if [ $((killed + lived)) -eq 0 ]; then
	if [ "$timed" -gt 0 ]; then
		echo "== all $timed mutants timed out, so none was judged =="
		exit 1
	fi
	echo "== no mutants on changed lines =="
	exit 0
fi
echo "== $killed killed, $lived lived, $timed timed out, floor $floor% =="
if [ $((killed * 100)) -lt $((floor * (killed + lived))) ]; then
	echo "lived (write a test that fails on each):"
	jq -r '.files[] | .file_name as $f | .mutations[] | select(.status == "LIVED") | "  \($f):\(.line):\(.column) \(.type)"' "$json"
	exit 1
fi
