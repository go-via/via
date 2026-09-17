# via — reactive web UIs in pure Go

> **Requires Go 1.27 or newer.** The public API uses generic methods
> (`ctx.Param[int]("id")`, `ctx.Session().Get[User]()`). On an older toolchain
> those read as ordinary syntax errors, not as a version complaint — check
> `go version` first if the example below will not compile.

via is a thin layer over `net/http`. Compositions nest as plain struct fields,
wired with `via.Child`, and via generates the Datastar attributes that make a
page live. The server renders the HTML; the browser is a rendering surface. No
build step, no hand-written JS, no WebSockets.

```go
package main

import (
	"log"
	"net/http"
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

type Store struct {
	mu sync.Mutex
	n  int
}

func (s *Store) Value() int { s.mu.Lock(); defer s.mu.Unlock(); return s.n }
func (s *Store) Add(d int)  { s.mu.Lock(); s.n += d; s.mu.Unlock() }

type Counter struct{ count *Store }

func (c *Counter) Inc(ctx *via.Ctx) { c.count.Add(1) }
func (c *Counter) Dec(ctx *via.Ctx) { c.count.Add(-1) }

func (c *Counter) View() h.H {
	return h.Div(
		h.H1(h.Str(c.count.Value())),
		h.Button(via.On("click", c.Dec), h.Str("-")),
		h.Button(via.On("click", c.Inc), h.Str("+")),
	)
}

func main() {
	store := &Store{}
	http.Handle("/", via.Handler(Counter{count: store}))
	log.Fatal(http.ListenAndServe(":8080", nil))
}
```

A composition is a struct. Its `View` is a pure, `ctx`-free function. Actions
are methods, wired by **named method value** (`via.On("click", c.Inc)`): no
strings, no closures. `via.Handler` takes the composition **by value**, so
there is no `&` at any call site, and a missing or mistyped `View` is a
compile error.

## The hard guarantees

- **No reflection in your wiring.** Nothing you write is bound by name: no
  tags, no method lookup by string, no struct shape you have to keep in sync
  with a template. Generics and interface assertions do the binding.
  Internally via does use `reflect` in two narrow places, both about *layout*,
  never about your identifiers: it takes a handler func value's code pointer
  for its action id, and it walks a composition's field offsets once per type
  to name signal slots and find child fields (`signalsOf`,
  `childFieldName`). Signal *values* decode through `encoding/json`, which
  reflects internally — that is data decoding, and the wiring stays
  reflection-free.
- **No user-facing identifier strings.** No `via:"name"` tags, no wire keys.
- **No closures at a via call site.** Named method values only.
- **No `any` in element/child signatures.** The `h.H` tree is sealed.
- **Zero `&` at any user call site.** via owns addressing.
- **`View` is pure and `ctx`-free.**

`TestExamples_takeNoAddressOfOrClosureAtViaCallSites` fails the build if an
example violates the `&`/closure rules, and the sealed `h.H` interface makes an
untyped node uninjectable.

## Documentation

[`DOCS.md`](./DOCS.md) — the tour, the plain/live model, the security floor,
shutdown, and the feature map with every trap.

[`MIGRATION.md`](./MIGRATION.md) — coming from v0.7.

## Develop

Requires Go 1.27+.

```bash
GO='env -u GOROOT /usr/bin/go' ./ci.sh   # fmt + vet + build + test -race
```

See [`CONVENTIONS.md`](./CONVENTIONS.md) for the test and code conventions.

`TestLive_onDisposeContinuesAfterAPanickingDisposer` is known-flaky under
heavy sweeps (`-race -cpu 1 -count=40` and up): a race in the test harness
between `synctest.Wait()` and real network I/O, predating this release, not
a product defect. CI gates at `-count=1`, which is green.
