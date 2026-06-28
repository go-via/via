# via/v2 — reactive web UIs in pure Go

A from-scratch reimagining of [via](https://github.com/go-via/via): a thin layer
over `net/http` that adds **effortless composition** and **Datastar sugar**, and
nothing you have to think about. The server renders HTML; the browser is a
rendering surface. No build step, no hand-written JS, no WebSockets.

```go
package main

import (
	"net/http"
	"sync"

	"github.com/go-via/via/v2"
	"github.com/go-via/via/v2/h"
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
		h.Button(via.OnClick(c.Dec), h.Str("-")),
		h.Button(via.OnClick(c.Inc), h.Str("+")),
	)
}

func main() {
	store := &Store{}
	http.Handle("/", via.Register(Counter{count: store}))
	http.ListenAndServe(":8080", nil)
}
```

A composition is a struct. Its `View` is a pure, `ctx`-free function. Actions are
methods, wired by **named method value** (`via.OnClick(c.Inc)`) — no strings, no
closures. `via.Register` takes the composition **by value**: there is no `&` at
any call site, and a missing or mistyped `View` is a compile error.

## The hard guarantees

- **No reflection in wiring.** The composition is bound by generics and
  interface assertions, never by reflecting over its fields, methods, or tags
  (a `reflect`-import lint enforces it). Signal *values* decode via
  `encoding/json`, which reflects internally — data decoding, not wiring.
- **No user-facing identifier strings.** No `via:"name"` tags, no wire keys.
- **No closures at a via call site.** Named method values only.
- **No `any` in element/child signatures.** The `h.H` tree is sealed.
- **Zero `&` at any user call site.** via owns addressing.
- **`View` is pure and `ctx`-free.**

These aren't slogans: `TestExamples_takeNoAddressOfOrClosureAtViaCallSites`
fails the build if an example violates the `&`/closure rules, and the sealed
`h.H` interface makes an untyped node uninjectable.

## Stateless by default, live by opt-in

A page is stateless request/response. A composition becomes a connection-scoped
**live island** (its own server state, pushed over SSE) the moment it implements
`OnConnect` — detected by interface assertion, never reflection. The same model
spans the spectrum from a fully static page to a fully live app. (Islands/SSE
are the next slices — see `ROADMAP.md`.)

## Security floor (built in)

The action endpoint and rendered pages are hardened by default:

- **Origin floor** on `POST` actions — same-origin (or an explicitly trusted
  origin) only, failing closed; `WithTrustedOrigin` / `WithInsecureOrigin`
  escape hatches.
- **Request-body cap** + strict decode (413 / 400), and a **panic recover**.
- **`nosniff` + a nonce'd CSP** on the page and patch responses. The CSP
  includes `'unsafe-eval'` because Datastar compiles `data-*` expressions with
  the `Function` constructor — without it every action is silently dead in the
  browser.
- **HTML/attribute escaping** with an attribute-name allowlist (`h.RawAttr` /
  `h.Data` reject injectable names).

## Status

Built and tested — `-race`-clean, adversarially reviewed, five runnable
examples, the whole live stack verified in real headless browsers
(`vtbrowser/`, `-tags browser`):

- **Hardened stateless core** (`example/counter`): by-value `Register`, origin
  floor, nonce'd CSP, body cap, panic-recover, compile-time `View` constraint,
  attribute-name allowlist. An action's response self-classifies — element-patch
  when the render changed, `204` when it didn't.
- **Reactive handles** (`example/greeting`): client-resident `Signal[T]` with
  handle-identity wire names — `Bind()` and `Display()` share one name, so the
  greeting updates live as you type, entirely client-side. `Local[T]` is a
  client-only signal (never round-trips); `If`/`When`/`Each` render conditionals
  and lists; `Counter.Op(ctx)` carries the numeric verbs.
- **Live islands + `State[T]`** (`example/pulse`): implement `OnConnect` and a
  composition becomes a live island with a per-tab SSE stream; `State[T]` is
  server-authoritative, read from the pure View and element-patched on change,
  `Tick` drives the push.
- **Interactive live actions** (`example/chat`): a live-island action routes —
  via the `via_tab` handshake (an unguessable per-connection id echoed in the
  `X-Via-Tab` header) — to *this* connection's island, mutates its state, and the
  result is pushed over its SSE. The element push omits `data-signals`, and
  deliberate signal changes ride a signal-patch, so a fan-out never clobbers what
  a user is typing.
- **Multi-user fan-out** (`example/feed`, `example/chat`): an in-process
  `via/topic.Topic[T]` broker + `via.Subscribe` / `ctx.OnDispose` — one publish
  fans out to every connected island.
- **Sessions** (`via/sess`, opt-in): `sess.Put[T]`/`Get[T]`/`Clear[T]` a typed
  per-browser store keyed by Go type (no tags, no reflection — a typed-nil
  sentinel), behind a signed-HMAC cookie issued lazily on first write;
  `sess.Rotate` for fixation defense, idle TTL eviction. Enabled by
  `WithSessionKey`/`WithSessionTTL`/`WithSessionCookieName`; apps that don't use
  it stay cookieless. The cookie is `Secure` automatically over TLS (so
  `http://localhost` dev still works); `WithSecureCookies` forces it on behind a
  TLS-terminating proxy.
- **Resilience floor + reconnect**: a server-side keepalive comment frame
  (`WithSSEHeartbeat`) and a per-frame write deadline (`WithSSEWriteTimeout`,
  default 10s) ride the island's single goroutine; a failed frame write tears the
  island down (runs disposers, stops ticks) so a half-open peer — gone without a
  FIN — can't leak its goroutine and timers. A client reconnect manager surfaces
  a "Reconnecting…" banner on a dropped stream and reloads to re-bootstrap when
  Datastar gives up; opt out with `WithoutSSEReconnect()`.
- **Live-island multiplexing** (`example/dashboard`): embed sub-compositions with
  `via.Child[C]` value-field handles — `p.Clock.Embed()` in the parent's `View`.
  A child without `OnConnect` is a plain in-place component; one with `OnConnect`
  is a live island, and all the live children on a page share the tab's *one* SSE
  stream on one goroutine — each re-renders and patches only its own region
  (`#via-i{n}`), its actions route by island id + the tab handshake, and its
  signals are slot-scoped so siblings never collide. `via.NewChild(child)` seeds a
  child's dependencies (a shared `*Topic`, a store) at registration.
- **Per-row list actions** (`example/poll`): a row's button carries the row's own
  datum — `via.OnClickArg(l.Delete, item.ID)` — and the handler receives it as a
  typed parameter, `func(*via.Ctx, int)`. Identity rides with the click, so a list
  that grows, shrinks, and **reorders** never misroutes: the value (not the
  positional slot) picks the row. Still a named method value — no `&`, no closure.

**The flagship is `example/chat`** — a live, multi-user chat room with a presence
count, in ~60 lines that read like a static page. Two-browser-verified: a message
typed in one tab appears in the other, the "N online" header tracks connections,
and the composer clears on send without clobbering a concurrent draft.

Deferred (correctly out of 1.0 scope): a keyed cursor for the narrow remaining
dynamic-shape cases — per-row *signals/inputs* in a **reordering** list, and
lists *of* live islands (per-row actions are done via `OnClickArg`; fixed
embeds via `via.Child[C]` are done); `via/router`; and at-least-once redelivery
(a push onto a dropping socket fails the write and tears down rather than being
buffered for replay). The SSE GET stream now applies the
same origin floor as the action POST and is capped at a configurable number of
concurrent connections (`WithMaxSSEConnections`, default 10,000; over the cap
returns 503). See [`DESIGN.md`](./DESIGN.md) and [`ROADMAP.md`](./ROADMAP.md).

## Develop

```bash
GO='env -u GOROOT /usr/bin/go' ./ci.sh   # fmt + vet + build + test -race
```

See [`CONVENTIONS.md`](./CONVENTIONS.md) for the test and code conventions.
