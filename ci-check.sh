#!/usr/bin/env bash
set -e
set -u
set -o pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

echo "== CI: Format code =="
go fmt ./...
echo "OK: formatting complete"

echo "== CI: Run go vet =="
if ! go vet ./...; then
  echo "ERROR: go vet failed."
  exit 1
fi
echo "OK: go vet passed"

echo "== CI: Build all packages =="
if ! go build ./...; then
  echo "ERROR: go build ./... failed."
  exit 1
fi
echo "OK: packages built"

echo "== CI: Build example apps under internal/examples =="
if [ -d "internal/examples" ]; then
  count=0
  while IFS= read -r -d '' mainfile; do
    dir="$(dirname "$mainfile")"
    echo "Building $dir"
    if ! (cd "$dir" && go build); then
      echo "ERROR: example build failed: $dir"
      exit 1
    fi
    count=$((count + 1))
  done < <(find internal/examples -type f -name "main.go" -print0)

  if [ "$count" -eq 0 ]; then
    echo "NOTE: no example main.go files found under internal/examples"
  else
    echo "OK: built $count example(s)"
  fi
else
  echo "NOTE: internal/examples not found, skipping example builds"
fi

echo "== CI: Run tests =="
if ! go test ./... 2>&1 | grep -v '\[no test files\]'; then
  echo "ERROR: tests failed."
  exit 1
fi

echo "SUCCESS: All checks passed."
exit 0
