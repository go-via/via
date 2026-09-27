<p align="center">
  <img src="internal/site/static/brand/punch-dark.png" alt="via" width="220">
</p>

# via

[![Go Reference](https://pkg.go.dev/badge/github.com/go-via/via.svg)](https://pkg.go.dev/github.com/go-via/via)
[![CI](https://github.com/go-via/via/actions/workflows/ci.yml/badge.svg)](https://github.com/go-via/via/actions/workflows/ci.yml)
[![CodeQL](https://github.com/go-via/via/actions/workflows/codeql.yml/badge.svg)](https://github.com/go-via/via/actions/workflows/codeql.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Live web UI in Go. A page is a struct, a click is a method call, and every
open tab sees the change. No JavaScript to write, no build step.

## A counter every tab shares

via needs Go 1.27 or newer; an older toolchain reports its generic methods as
syntax errors, not as a version mismatch.

```bash
go mod init example.com/counter
go get github.com/go-via/via
```

`main.go`:

```go
package main

import (
	"log"
	"net/http"
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/topic"
)

var (
	mu    sync.Mutex
	count int64
	moved = topic.New[int64]()
)

func add(n int64) {
	mu.Lock()
	defer mu.Unlock()
	count += n
	moved.Publish(count)
}

func load() int64 {
	mu.Lock()
	defer mu.Unlock()
	return count
}

type Counter struct{ N via.State[int64] }

func (c *Counter) Inc(ctx *via.Ctx) { add(1) }
func (c *Counter) Dec(ctx *via.Ctx) { add(-1) }

func (c *Counter) View() h.H {
	return h.Div(
		h.Button(on.Click(c.Dec), h.Str("-")),
		h.H1(c.N.Display()),
		h.Button(on.Click(c.Inc), h.Str("+")),
	)
}

func main() {
	r := via.Handler(Counter{N: via.StateTrack(moved, load)})
	err := http.ListenAndServe(":8080", r)
	r.Close()
	log.Fatal(err)
}
```

```bash
go run .
```

Open http://localhost:8080 in two tabs and click + in one. Both update.
[Getting started](https://go-via.dev/start) walks through every line.

## Why via

- **Handlers are methods.** `on.Click(c.Inc)` takes a method value, so a
  misspelled handler is a compile error, not a dead button.
- **Live only where it needs to be.** A page is plain HTTP until it renders
  live state; then its tab holds one stream.
  [Live state](https://go-via.dev/live)
- **Nothing to build.** The browser client ships inside the module, and the
  markup is Go.
- **Testable from `go test`.** Package `vt` drives pages, actions and live
  streams without a browser. [Testing](https://go-via.dev/testing)

## What it costs

Live state lives on the server: each live tab holds memory and a goroutine,
and state stays in one process unless you bridge it across replicas.
[What it costs](https://go-via.dev/why#what-it-costs) has the full list.

## Documentation

- [Getting started](https://go-via.dev/start)
- [Tutorial](https://go-via.dev/tutorial)
- [Examples](https://go-via.dev/examples), with their source in
  [`internal/example`](internal/example/README.md)
- [API reference](https://go-via.dev/reference), and
  [pkg.go.dev](https://pkg.go.dev/github.com/go-via/via)
- [Deploy](https://go-via.dev/deploy)
- [Migrating](https://go-via.dev/migrate), with the full text in
  [`MIGRATION.md`](MIGRATION.md)

## Status

Pre-1.0: the API can change between minor versions.
[`CHANGELOG.md`](CHANGELOG.md) records each release.

## Contributing

```bash
./ci.sh
```

It runs gofmt, vet, staticcheck and the race tests for via and the site
module (`internal/site`), then the browser tier in `vtbrowser`, which needs
Chromium or Chrome; `--no-browser` skips it. It reports, never rewrites.

[`CONVENTIONS.md`](CONVENTIONS.md) has the code and test conventions, and
[`AGENTS.md`](AGENTS.md) the bar for a new duck-typed method.

## License

MIT, see [`LICENSE`](LICENSE).
