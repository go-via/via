# via — reactive web UIs in pure Go

via is a thin layer over `net/http` that adds **effortless composition** and
**Datastar sugar**, and nothing you have to think about. The server renders
HTML; the browser is a rendering surface. No build step, no hand-written JS,
no WebSockets.

```go
package main

import (
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
spans the spectrum from a fully static page to a fully live app.

## Security floor (built in)

The action endpoint and rendered pages are hardened by default:

- **Origin floor** on `POST` actions — open by default (the per-tab id is the
  CSRF token, and dev/non-browser clients just work); set `WithTrustedOrigin`
  in production to enforce same-origin (plus the listed origins), failing
  closed.
- **Request-body cap** + strict decode (413 / 400), and a **panic recover**.
- **`nosniff` + a nonce'd CSP** on the page and patch responses. The CSP
  includes `'unsafe-eval'` because Datastar compiles `data-*` expressions with
  the `Function` constructor — without it every action is silently dead in the
  browser. The nonce is a **boot nonce** — `HMAC(session key, "via/csp-nonce")`
  — deliberately stable per key, so a `via.Redirect` from a `@post` action
  ships a `location.assign()` script every document this app (or any pod
  sharing `VIA_SESSION_KEY`) served will admit, cookieless first request
  included. Honest posture: the CSP is a seatbelt against *injected* inline
  script; the load-bearing defenses are output escaping, the attribute-name
  allowlist, and the `h.SafeURL` gate on every redirect target
  (`javascript:`/`data:`/`//` are dropped loudly, falling back to the element
  patch).
- **HTML/attribute escaping** with an attribute-name allowlist (`h.RawAttr` /
  `h.Data` reject injectable names).

## Status

Built and tested — `-race`-clean, adversarially reviewed, eight runnable
examples, the whole live stack verified in real headless browsers
(`vtbrowser/`, `-tags browser`):

- **Hardened stateless core** (`example/counter`): by-value `Register`, origin
  floor, nonce'd CSP, body cap, panic-recover, compile-time `View` constraint,
  attribute-name allowlist. An action's response self-classifies — element-patch
  when the render changed, `204` when it didn't.
- **Reactive handles** (`example/greeting`): client-resident `Signal[T]` with
  handle-identity wire names — `Bind()` and `Display()` share one name, so the
  greeting updates live as you type, entirely client-side. `Local[T]` is a
  client-only signal (never round-trips); `When`/`Each` render conditionals
  and lists.
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
- **Sessions** (always available): `via.SessPut[T]`/`SessGet[T]`/`SessClear[T]`
  a typed per-browser store keyed by Go type (no tags, no reflection — a
  typed-nil sentinel), behind a signed-HMAC cookie issued lazily on the first
  write — apps that never store anything stay cookieless. `via.SessRotate` for
  fixation defense, idle TTL eviction. The signing key resolves
  `WithSessionKey` → `VIA_SESSION_KEY` env → a random per-process key (warned on
  first use — set a stable key so sessions survive restarts and span pods).
  `WithSessionTTL`/`WithSessionCookieName` tune it. The cookie is `Secure`
  automatically over TLS (so `http://localhost` dev still works);
  `WithSecureCookies` forces it on behind a TLS-terminating proxy.
- **Resilience floor + reconnect**: a server-side keepalive comment frame
  (`WithSSEHeartbeat`) and a per-frame write deadline (`WithSSEWriteTimeout`,
  default 10s) ride the island's single goroutine; a failed frame write tears the
  island down (runs disposers, stops ticks) so a half-open peer — gone without a
  FIN — can't leak its goroutine and timers. A client reconnect manager surfaces
  a "Reconnecting…" banner on a dropped stream and reloads to re-bootstrap when
  Datastar gives up.
- **Live-island multiplexing** (`example/dashboard`): embed sub-compositions as
  plain struct fields — `via.Embed(p.Clock)` in the parent's `View`. A child
  without `OnConnect` is a plain in-place component; one with `OnConnect` is a
  live island, and all the live children on a page share the tab's *one* SSE
  stream on one goroutine — each re-renders and patches only its own region
  (`#via-i{n}`), its actions route by island id + the tab handshake, and its
  signals are slot-scoped so siblings never collide. The parent's literal seeds a
  child's dependencies (a shared `*Topic`, a store) at registration; generic
  layouts (`Shell[C]{Body C}`) compose one shell with any page. Ownership is
  by value: the field literal seeds the child, each connection gets its own
  copy (value state stays per-tab), and pointer deps are the deliberate
  sharing channel.
- **Per-row list actions** (`example/poll`): a row's button carries the row's own
  datum — `via.OnClickArg(l.Delete, item.ID)` — and the handler receives it as a
  typed parameter, `func(*via.Ctx, int)`. Identity rides with the click, so a list
  that grows, shrinks, and **reorders** never misroutes: the value (not the
  positional slot) picks the row. Still a named method value — no `&`, no closure.

- **Multi-page apps + auth + uploads** (`example/forum`): `via.NewRouter()` with
  `via.Mount(r, "/path", Page{}, guards...)` serves a whole app behind one
  handler, each page's actions namespaced under its mount. `OnInit(*Ctx) error`
  is the per-request hook that loads session/path data into a stateless page
  before its ctx-free `View` — return `via.ErrNotFound` for a vanished record
  (404); any other error answers 500, and the View never renders a lie. `via.PostForm(handler, …)` renders a **native** form whose
  submit runs server-side and `via.Redirect(ctx, "/…")` issues a 303 — the
  server-rendered auth flow the bundled Datastar (no script execution) can't do.
  `via.Param[int](ctx, 0)` reads the positional `{}` segment of `"/thread/{}"`;
  `via.RequireSession[User]("/login")` is a guard *value* (no closure) that
  bounces anonymous visitors. `via.OnUpload(handler, …)` + `via.File` handle the
  avatar — the one form that posts real multipart, handed to the app as an
  `io.Reader` it stores however it likes. The forum proves these compose into
  a full multi-page app.

**The flagship is `example/chat`** — a live, multi-user chat room with a presence
count, in ~60 lines that read like a static page. Two-browser-verified: a message
typed in one tab appears in the other, the "N online" header tracks connections,
and the composer clears on send without clobbering a concurrent draft.

**Restarts and deploys.** Sessions and the boot CSP nonce both derive from the
signing key, so with a stable key (`WithSessionKey` / `VIA_SESSION_KEY`) a
restart or a rolling deploy keeps cookies valid and redirect scripts admitted
across pods. Live-island state is in-memory and per-connection: a deploy drops
the stream, the client reconnect manager shows "Reconnecting…" and reloads to
re-bootstrap — the page comes back from server truth, not from replayed frames.
Error pages are plain `http.Error` text for now (404 for `via.ErrNotFound` /
a decode-miss `Param`, 500 for the rest); a `WithErrorPage` hook is post-1.0.

Deferred (correctly out of 1.0 scope): a keyed cursor for the narrow remaining
dynamic-shape cases — per-row *signals/inputs* in a **reordering** list, and
lists *of* live islands (per-row actions are done via `OnClickArg`; fixed
embeds via `via.Embed` are done); and at-least-once redelivery (a push onto
a dropping socket fails the write and tears down rather than being buffered
for replay). The SSE GET stream applies the same origin floor as the action
POST and is capped at a configurable number of concurrent connections
(`WithMaxSSEConnections`, default 10,000; over the cap returns 503).

## Develop

```bash
GO='env -u GOROOT /usr/bin/go' ./ci.sh   # fmt + vet + build + test -race
```

See [`CONVENTIONS.md`](./CONVENTIONS.md) for the test and code conventions.
