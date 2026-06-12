#!/usr/bin/env bash
set -e
set -u
set -o pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

# --browser runs ONLY the real-browser suite and exits. vtbrowser is its
# own module, so the default `go test ./...` below never reaches it; CI
# runs this in a dedicated job with Chromium installed, and
# VIA_BROWSER_REQUIRED=1 turns the no-browser skip into a hard failure
# so CI cannot silently skip the suite.
if [ "${1:-}" = "--browser" ]; then
  echo "== CI: Browser tests (vtbrowser) =="
  (cd vtbrowser && VIA_BROWSER_REQUIRED=1 go test -race ./...)
  echo "SUCCESS: browser tests passed."
  exit 0
fi

# Pinned tool versions. Bump deliberately; @latest in CI breaks reproducibility.
GOLANGCI_VERSION="${GOLANGCI_VERSION:-v2.12.2}"
GOVULNCHECK_VERSION="${GOVULNCHECK_VERSION:-v1.1.4}"
APIDIFF_VERSION="${APIDIFF_VERSION:-v0.0.0-20260603202125-055de637280b}"

# Allocation thresholds for bench gates. Bumped if intentional regressions
# land in a feature commit; tightened when a perf commit lands. Keep
# generous-but-not-loose so noise doesn't fail CI.
#
# Current floors (steady state on the bench page in bench_test.go):
#   CounterRender              ~181 allocs/op  (the always-injected client
#                                               reconnect manager + its
#                                               connection-status hook add a
#                                               data-init blob to every head)
#   CounterAction              ~129 allocs/op
#   CounterActionWithLogger    ~129 allocs/op  (logger path must stay flat
#                                               vs CounterAction)
RENDER_ALLOC_MAX=${RENDER_ALLOC_MAX:-185}
ACTION_ALLOC_MAX=${ACTION_ALLOC_MAX:-149}
LOGGER_ACTION_ALLOC_MAX=${LOGGER_ACTION_ALLOC_MAX:-149}

echo "== CI: Check formatting =="
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "ERROR: files need 'gofmt -w':"
  echo "$unformatted"
  exit 1
fi
echo "OK: gofmt clean"

echo "== CI: No committed binaries =="
# Compiled executables (e.g. a stray `go build` output) must never be
# committed — they bloat history permanently and .gitignore only guards
# against accident. Match machine-binary mime types, so shell scripts
# (text/x-shellscript) and images (image/*) are not flagged.
binaries=$(git ls-files -z | xargs -0 file --mime-type 2>/dev/null |
  grep -E ': application/(x-executable|x-pie-executable|x-sharedlib|x-mach-binary|x-dosexec|x-elf)$' || true)
if [ -n "$binaries" ]; then
  echo "ERROR: committed compiled binaries detected:"
  echo "$binaries"
  exit 1
fi
echo "OK: no committed binaries"

echo "== CI: Run go vet =="
go vet ./...
echo "OK: go vet passed"

echo "== CI: golangci-lint =="
if ! command -v golangci-lint >/dev/null 2>&1; then
  go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_VERSION}"
fi
golangci-lint run ./...
echo "OK: golangci-lint passed"

echo "== CI: govulncheck =="
if ! command -v govulncheck >/dev/null 2>&1; then
  go install "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}"
fi
govulncheck ./...
echo "OK: govulncheck passed"

echo "== CI: Build all packages =="
go build ./...
echo "OK: packages built"

echo "== CI: Build example apps under internal/examples =="
if [ -d "internal/examples" ]; then
  count=0
  while IFS= read -r -d '' mainfile; do
    dir="$(dirname "$mainfile")"
    echo "Building $dir"
    (cd "$dir" && go build -o /tmp)
    count=$((count + 1))
  done < <(find internal/examples -type f -name "main.go" -print0)
  echo "OK: built $count example(s) to /tmp"
else
  echo "NOTE: internal/examples not found, skipping example builds"
fi

echo "== CI: API compatibility =="
# Mechanically enforce the public stability contract (docs/stability.md):
# fail on any INCOMPATIBLE change to an exported, importable package vs
# its committed golden baseline under api/. Compatible (additive) changes
# do NOT fail — they are landed freely.
#
# apidiff is whole-package: it cannot subset stable-Core from EXPERIMENTAL
# symbols. So this gate is a "no silent public break" detector. An
# intentional public change is landed by regenerating the baseline
# (./api/regen.sh), which surfaces it in the PR diff; the Core-vs-
# experimental call is made there in review against docs/stability.md.
#
# NOTE: apidiff exits 0 even when it reports incompatible changes, so we
# parse its output and fail closed on the "Incompatible changes:" header.
if ! command -v apidiff >/dev/null 2>&1; then
  go install "golang.org/x/exp/cmd/apidiff@${APIDIFF_VERSION}"
fi
api_failed=0
for baseline in api/*.api; do
  case "$(basename "$baseline")" in
    via.api) pkg="github.com/go-via/via" ;;
    h.api) pkg="github.com/go-via/via/h" ;;
    on.api) pkg="github.com/go-via/via/on" ;;
    sess.api) pkg="github.com/go-via/via/sess" ;;
    mw.api) pkg="github.com/go-via/via/mw" ;;
    *) echo "ERROR: no package mapping for baseline $baseline"; exit 1 ;;
  esac
  out=$(apidiff "$baseline" "$pkg" 2>&1)
  if echo "$out" | grep -q '^Incompatible changes:'; then
    echo "ERROR: INCOMPATIBLE API change in $pkg vs $baseline:"
    echo "$out"
    echo "If intentional, run ./api/regen.sh and review the diff against docs/stability.md."
    api_failed=1
  else
    echo "OK: $pkg API compatible with $baseline"
  fi
done
if [ "$api_failed" -ne 0 ]; then
  exit 1
fi
echo "OK: public API compatible with committed baselines"

echo "== CI: Run tests =="
go test -race ./... 2>&1 | grep -v '\[no test files\]'

echo "== CI: Allocation gates =="
# Bench output looks like:
#   BenchmarkCounterRender-20    1000   95012 ns/op   29200 B/op   206 allocs/op
# Pull the allocs column for the named benchmarks and fail if it
# exceeds the threshold for that bench.
# Capture the bench exit status explicitly: a compile error, panic, or
# missing benchmark must fail the gate, not skip it (fail closed).
bench_status=0
bench_out=$(go test ./. -run='^$' -bench='^BenchmarkCounter' -benchtime=200x -benchmem 2>&1) || bench_status=$?
echo "$bench_out"
if [ "$bench_status" -ne 0 ]; then
  echo "ERROR: allocation benchmarks failed to run (exit $bench_status); perf gate cannot be evaluated"
  exit 1
fi

check_alloc() {
  local name=$1
  local threshold=$2
  local line
  line=$(echo "$bench_out" | grep -E "^${name}-" || true)
  if [ -z "$line" ]; then
    echo "ERROR: bench $name not found in output; perf gate cannot be evaluated"
    return 1
  fi
  local got
  got=$(echo "$line" | awk '{for(i=1;i<=NF;i++) if($i=="allocs/op") print $(i-1)}')
  if [ -z "$got" ]; then
    echo "ERROR: could not parse allocs/op from: $line"
    return 1
  fi
  if [ "$got" -gt "$threshold" ]; then
    echo "ERROR: $name regressed to $got allocs/op (threshold: $threshold)"
    return 1
  fi
  echo "OK: $name = $got allocs/op (threshold: $threshold)"
}

check_alloc BenchmarkCounterRender "$RENDER_ALLOC_MAX"
check_alloc BenchmarkCounterAction "$ACTION_ALLOC_MAX"
check_alloc BenchmarkCounterActionWithLogger "$LOGGER_ACTION_ALLOC_MAX"

echo "SUCCESS: All checks passed."
exit 0
