#!/usr/bin/env bash
set -e
set -u
set -o pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

# Pinned tool versions. Bump deliberately; @latest in CI breaks reproducibility.
GOLANGCI_VERSION="${GOLANGCI_VERSION:-v2.12.2}"
GOVULNCHECK_VERSION="${GOVULNCHECK_VERSION:-v1.1.4}"

# Allocation thresholds for bench gates. Bumped if intentional regressions
# land in a feature commit; tightened when a perf commit lands. Keep
# generous-but-not-loose so noise doesn't fail CI.
#
# Current floors (steady state on the bench page in bench_test.go):
#   CounterRender              ~164 allocs/op  (post-gomponents-free h pkg)
#   CounterAction              ~129 allocs/op
#   CounterActionWithLogger    ~129 allocs/op  (logger path must stay flat
#                                               vs CounterAction)
RENDER_ALLOC_MAX=${RENDER_ALLOC_MAX:-180}
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
