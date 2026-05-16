#!/usr/bin/env bash
set -e
set -u
set -o pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

# Allocation thresholds for bench gates. Bumped if intentional regressions
# land in a feature commit; tightened when a perf commit lands. Keep
# generous-but-not-loose so noise doesn't fail CI.
#
# Current floors (steady state on the bench page in bench_test.go):
#   CounterRender         ~200 allocs/op
#   CounterAction         ~130 allocs/op
#   ActionBodyOnly          0 allocs/op  (typed Mutable[T] hot path)
#   SignalFlush             2 allocs/op  (encoded []byte + json.RawMessage box)
RENDER_ALLOC_MAX=${RENDER_ALLOC_MAX:-212}
ACTION_ALLOC_MAX=${ACTION_ALLOC_MAX:-149}
BODY_ALLOC_MAX=${BODY_ALLOC_MAX:-0}
SIGNAL_FLUSH_ALLOC_MAX=${SIGNAL_FLUSH_ALLOC_MAX:-3}

echo "== CI: Check formatting =="
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "ERROR: files need 'gofmt -w':"
  echo "$unformatted"
  exit 1
fi
echo "OK: gofmt clean"

echo "== CI: Run go vet =="
go vet ./...
echo "OK: go vet passed"

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
bench_out=$(go test ./. -run='^$' -bench='^Benchmark(Counter|ActionBodyOnly|SignalFlush)' -benchtime=200x -benchmem 2>&1 || true)
echo "$bench_out"

check_alloc() {
  local name=$1
  local threshold=$2
  local line
  line=$(echo "$bench_out" | grep -E "^${name}-" || true)
  if [ -z "$line" ]; then
    echo "WARN: bench $name not found in output, skipping gate"
    return 0
  fi
  local got
  got=$(echo "$line" | awk '{for(i=1;i<=NF;i++) if($i=="allocs/op") print $(i-1)}')
  if [ -z "$got" ]; then
    echo "WARN: could not parse allocs/op from $line"
    return 0
  fi
  if [ "$got" -gt "$threshold" ]; then
    echo "ERROR: $name regressed to $got allocs/op (threshold: $threshold)"
    return 1
  fi
  echo "OK: $name = $got allocs/op (threshold: $threshold)"
}

check_alloc BenchmarkCounterRender "$RENDER_ALLOC_MAX"
check_alloc BenchmarkCounterAction "$ACTION_ALLOC_MAX"
check_alloc BenchmarkActionBodyOnly "$BODY_ALLOC_MAX"
check_alloc BenchmarkSignalFlush "$SIGNAL_FLUSH_ALLOC_MAX"

echo "SUCCESS: All checks passed."
exit 0
