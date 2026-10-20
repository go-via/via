#!/usr/bin/env bash
# PR mutation gate, run from the repo root: mutates the lines changed since
# BASE in each module and fails when the tests kill fewer of those mutants than
# the module's floor. A floor is the module's efficacy on the first full run
# (2026-10-20); raise it by hand as tests improve, never lower it.
set -euo pipefail

GO="${GO:-go}"
gremlins="$GO run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0"

if [ $# -ne 1 ]; then
	echo "usage: mutation.sh BASE (the git ref the PR merges into, e.g. origin/main)" >&2
	exit 2
fi
base="$1"
root="$PWD"
out="$(mktemp -d /tmp/mutation.XXXXXX)"
trap 'rm -rf "$out"' EXIT

# gremlins v0.6.0 counts a mutant that fails to build as killed. Its arithmetic
# mutator turns string + into -, which never builds, so it is off. Its
# --threshold-efficacy flag is ignored, so score() reads the JSON instead.
# --integration runs the whole module's tests per mutant; without it a package
# counts only its own tests. The timeout is a multiple of the coverage run, so
# -count=1 keeps that run out of the test cache, where it takes milliseconds
# and every mutant then times out.
flags=(unleash --integration --coverpkg ./... --timeout-coefficient 30 --arithmetic-base=false --diff "$base")
export GOFLAGS="-count=1"

# score fails when killed/(killed+lived) is under the floor, listing what lived.
# It runs under ||, where set -e is off, so a missing report fails explicitly.
score() {
	local name="$1" json="$out/$2.json" floor="$3" killed lived timed
	if ! [ -s "$json" ]; then
		echo "== $name: gremlins wrote no report =="
		return 1
	fi
	killed="$(jq '[.files[]?.mutations[] | select(.status == "KILLED")] | length' "$json")"
	lived="$(jq '[.files[]?.mutations[] | select(.status == "LIVED")] | length' "$json")"
	timed="$(jq '[.files[]?.mutations[] | select(.status == "TIMED OUT")] | length' "$json")"
	if [ $((killed + lived)) -eq 0 ] && [ "$timed" -gt 0 ]; then
		echo "== $name: all $timed mutants timed out, so none was judged =="
		return 1
	fi
	if [ $((killed + lived)) -eq 0 ]; then
		echo "== $name: no mutants on changed lines =="
		return 0
	fi
	echo "== $name: $killed killed, $lived lived, $timed timed out, floor $floor% =="
	if [ $((killed * 100)) -lt $((floor * (killed + lived))) ]; then
		echo "lived (write a test that fails on each):"
		jq -r '.files[] | .file_name as $f | .mutations[] | select(.status == "LIVED") | "  \($f):\(.line):\(.column) \(.type)"' "$json"
		return 1
	fi
}

fail=0

echo "== .: gremlins =="
$gremlins "${flags[@]}" -E '^(internal/site|vtbrowser|\.research)/' -o "$out/root.json" .
score . root 85 || fail=1

# gremlins copies a nested module alone into TMPDIR, so its replace => ../..
# and its reads of the repo's docs miss. With TMPDIR at the repo root the copy
# sits two levels down, where ../.. is the repo again. diff.relative makes
# --diff paths match the module's own.
echo "== internal/site: gremlins =="
(
	cd internal/site
	TMPDIR="$root" GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=diff.relative GIT_CONFIG_VALUE_0=true \
		$gremlins "${flags[@]}" -E '^snippet/src/' -o "$out/site.json" .
)
score internal/site site 68 || fail=1

# vtbrowser's replace points at ../, so it gets a modfile that points at the
# repo by absolute path. TMPDIR stays short: Chromium's socket path has a cap.
echo "== vtbrowser: gremlins =="
cp vtbrowser/go.mod "$out/vtb.mod"
cp vtbrowser/go.sum "$out/vtb.sum"
$GO mod edit -replace "github.com/go-via/via=$root" "$out/vtb.mod"
(
	cd vtbrowser
	TMPDIR=/tmp GOFLAGS="$GOFLAGS -modfile=$out/vtb.mod" GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=diff.relative GIT_CONFIG_VALUE_0=true \
		$gremlins "${flags[@]}" --tags browser --workers 4 -o "$out/vtb.json" .
)
score vtbrowser vtb 88 || fail=1

exit "$fail"
