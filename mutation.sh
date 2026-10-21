#!/usr/bin/env bash
# PR mutation gate, run from the repo root: mutates the lines changed since
# BASE in the root module and fails when the tests kill fewer of those mutants
# than the floor. The floor is root's efficacy on the first full run
# (2026-10-20); raise it by hand as tests improve, never lower it.
# internal/site (the docs site) and vtbrowser (a test harness) are left out:
# ci.sh tests both, and mutating them bought noise, not confidence. They are
# nested modules, so ./... already skips them.
set -euo pipefail

GO="${GO:-go}"
floor=76

if [ $# -ne 1 ]; then
	echo "usage: mutation.sh BASE (the git ref the PR merges into, e.g. origin/main)" >&2
	exit 2
fi
out="$(mktemp -d /tmp/mutation.XXXXXX)"
trap 'rm -rf "$out"' EXIT
json="$out/root.json"

# Built, not go run: go run reports the tool's exit 10 (under the floor) as 1.
GOBIN="$out" $GO install github.com/gomutants/gomutants@v0.7.0

# --integration runs the tests of every package that imports the mutated one;
# without it a package counts only its own tests. --cache=off keeps
# .gomutants-cache.json out of the tree. --detect-equivalent drops mutants the
# compiler folds away, which no test can kill; a mutant it misses gets a
# "// gomutants:disable-next-line <MUTATOR> reason=..." line. The 2s default
# timeout floor times mutants out on a loaded machine. Each run of via's suite
# leaves hundreds of loopback sockets in TIME_WAIT; at 20 workers they exhaust
# the ephemeral ports, httptest cannot listen, and gomutants reads that as tests
# that fail together. 4 workers stay clear and match a GitHub runner's CPUs.
flags=(--integration --cache=off --detect-equivalent --timeout-min 30s --workers 4
	--changed-since "$1" --threshold-efficacy "$floor" -o "$json")
if [ -n "${GITHUB_ACTIONS:-}" ]; then
	flags+=(--annotations github)
fi
rc=0
"$out/gomutants" "${flags[@]}" ./... || rc=$?

# The threshold is skipped when nothing was judged, so a run where every
# mutant timed out would pass. The report can leave out a zero count.
if [ "$rc" -eq 0 ] && [ -s "$json" ] &&
	[ "$(jq '(.mutants_killed // 0) + (.mutants_lived // 0)' "$json")" -eq 0 ] &&
	[ "$(jq '.mutants_timed_out // 0' "$json")" -gt 0 ]; then
	echo "mutation.sh: every mutant timed out, so none was judged" >&2
	exit 1
fi
if [ "$rc" -eq 10 ]; then
	echo "lived (write a test that fails on each):"
	jq -r '.files[] | .file_name as $f | .mutations[] | select(.status == "LIVED") | "  \($f):\(.line):\(.column) \(.type)"' "$json"
fi
exit "$rc"
