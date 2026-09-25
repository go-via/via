<p align="center">
  <img src="internal/site/static/brand/punch-dark.png" alt="via" width="220">
</p>

# via

[![Go Reference](https://pkg.go.dev/badge/github.com/go-via/via.svg)](https://pkg.go.dev/github.com/go-via/via)
[![CodeQL](https://github.com/go-via/via/actions/workflows/codeql.yml/badge.svg)](https://github.com/go-via/via/actions/workflows/codeql.yml)
[![CI](https://github.com/go-via/via/actions/workflows/ci.yml/badge.svg)](https://github.com/go-via/via/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Docs](https://img.shields.io/badge/docs-go--via.dev-blue)](https://go-via.dev)

A Go library for server-rendered, live web UI: HTML from Go functions,
actions as methods, no JavaScript to write.

A page is a struct, a click is a method call, and via streams what changed
back to the tab. No build step, and plain HTTP until something on the page
ticks.

## Run a counter

via needs Go 1.27 or newer. Its API uses generic methods
(`ctx.Param[int]("id")`); an older toolchain reports those as syntax errors,
not as a version mismatch.

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
	"sync/atomic"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

type Counter struct{ n *atomic.Int64 }

func (c *Counter) Inc(ctx *via.Ctx) { c.n.Add(1) }
func (c *Counter) Dec(ctx *via.Ctx) { c.n.Add(-1) }

func (c *Counter) View() h.H {
	return h.Div(
		h.Button(on.Click(c.Dec), h.Str("-")),
		h.H1(h.Str(c.n.Load())),
		h.Button(on.Click(c.Inc), h.Str("+")),
	)
}

func main() {
	r := via.Handler(Counter{n: new(atomic.Int64)})
	err := http.ListenAndServe(":8080", r)
	r.Close()
	log.Fatal(err)
}
```

```bash
go run .
```

Open http://localhost:8080 and click +. The button POSTs to `Inc`, via renders
`Counter` again, and the page morphs the new number in place.

## How it works

- **A page is a struct.** Its `View` is a pure, ctx-free method that returns
  markup built from Go functions in package `h`.
  [Compositions](https://go-via.dev/compositions)
- **An action is a method.** [`on.Click`](https://go-via.dev/reference#on.Click) takes the method value
  `c.Inc`, so a misspelled handler or event does not compile.
  [Actions](https://go-via.dev/actions)
- **The field type decides where state lives.** [`via.Signal`](https://go-via.dev/reference#via.Signal)
  lives in the browser and travels with each action POST;
  [`via.State`](https://go-via.dev/reference#via.State) lives on the server, one copy per tab, and never
  reaches the client as data. [Signals](https://go-via.dev/signals)
- **A page streams only if it holds a live unit**: one that renders a `State`
  or `List`, or registers [`Tick`](https://go-via.dev/reference#via.Ctx.Tick) or
  [`Listen`](https://go-via.dev/reference#via.Ctx.Listen). That tab then opens one SSE stream.
  Otherwise a GET is a response and an action is a POST.
  [Live state](https://go-via.dev/live)
- **No JavaScript and no build step.** The Datastar client is embedded in the
  module and served by the router. A JS library gets one subtree as an
  [island](https://go-via.dev/islands).
- **Nothing binds by name.** No string tags, no method lookup by string, no
  `&` and no closures at a via call site.
  [`sourcelint_test.go`](sourcelint_test.go) fails the tests if via's own
  reflection or an example breaks those rules.

## Make it live

In the counter above a second tab keeps the old number until its own next
click. Move the count to a store, announce each change on a
[`topic.Topic`](https://go-via.dev/reference#topic.Topic) and render it as a `via.State`: each tab opens
one SSE stream and every publish reaches it. The count and the topic live in
one process's memory, so two replicas each keep their own.
[Getting started: Make it live](https://go-via.dev/start#make-it-live) has the
program and a running copy.

## Documentation

| Page | Covers |
| --- | --- |
| [Getting started](https://go-via.dev/start) | Install, the counter, making it live |
| [Tutorial: live chat](https://go-via.dev/tutorial) | `internal/example/chat` in five runnable steps |
| [Examples](https://go-via.dev/examples) | Whole programs; source in [`internal/example`](internal/example/README.md) |
| [API reference](https://go-via.dev/reference) | Every exported name, in tables; also [pkg.go.dev](https://pkg.go.dev/github.com/go-via/via) |
| [Deploy](https://go-via.dev/deploy) | Shutdown order, proxies, sticky routing, a cross-pod topic bridge |
| [Migrating from v0.7](https://go-via.dev/migrate) | The v0.7 → v0.8 mapping; [`MIGRATION.md`](MIGRATION.md) is the full text |

## Status

Pre-1.0: APIs change between minor versions. The current release is v0.8.2;
v0.8.0 is retracted. Deprecated names stay until the next minor version
(`via.On`, `via.OnArg` and `expr.Lit` are removed in v0.9).
[`CHANGELOG.md`](CHANGELOG.md) is the release-by-release record, and
[Why Via](https://go-via.dev/why#what-it-costs) lists the costs: memory and a
goroutine per live tab, `'unsafe-eval'` in the CSP, one process unless you
bridge.

## Contributing

```bash
GO='env -u GOROOT /usr/bin/go' ./ci.sh   # fmt + vet + build + test -race
```

[`CONVENTIONS.md`](CONVENTIONS.md) has the test and code conventions and
[`AGENTS.md`](AGENTS.md) the rule for when a property becomes a duck-typed
method.

`TestLive_onDisposeContinuesAfterAPanickingDisposer` is known-flaky under
heavy sweeps (`-race -cpu 1 -count=40` and up): a race in the test harness
between `synctest.Wait()` and real network I/O, not a product defect. CI gates
at `-count=1`, which is green.

## License

MIT, see [`LICENSE`](LICENSE).
