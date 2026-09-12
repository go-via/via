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
		h.Button(via.On("click", c.Dec), h.Str("-")),
		h.Button(via.On("click", c.Inc), h.Str("+")),
	)
}

func main() {
	store := &Store{}
	http.Handle("/", via.Register(Counter{count: store}))
	http.ListenAndServe(":8080", nil)
}
```

A composition is a struct. Its `View` is a pure, `ctx`-free function. Actions are
methods, wired by **named method value** (`via.On("click", c.Inc)`) — no strings, no
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

## Stateless by default, live by what a unit does

A page is stateless request/response. It becomes a connection-scoped **live
island** (its own server state, pushed over SSE) the moment it *acts* like
one — its `OnInit` registered a `ctx.Tick` or a `ctx.Listen`, or its `View`
rendered a `State[T]`/`List[E]`. There is no marker interface and no second
hook: `Initer`/`OnInit` is the one lifecycle hook, on a page and on every
embedded child. The same model spans the spectrum from a fully static page to a
fully live app.

`OnInit` runs on every request that renders the unit — the GET, each action,
the SSE connect. Register connection-scoped side effects rather than performing
them: `ctx.OnLive(fn)` runs once when the stream opens, `ctx.OnDispose(fn)`
when it closes.

## Security floor (built in)

The action endpoint and rendered pages are hardened by default:

- **Origin floor** on `POST` actions — open by default (the per-tab id is the
  CSRF token, and dev/non-browser clients just work); set `WithTrustedOrigin`
  in production to enforce same-origin (plus the listed origins), failing
  closed.
- A live connection's tab id is a bearer credential for that connection's
  actions; if it was opened under a session, a dispatch is also checked
  against that same session, so a leaked tab id alone is no longer enough
  once the connecting browser was logged in. An anonymous connection (no
  session) has nothing to check against — never render, log, or leak a tab
  id outside its own client, and set `WithTrustedOrigin` to close the
  cross-origin leg too.
- **Session fixation defense is opt-in, not automatic**: sessions never
  rotate their id on their own, so call `Session.Rotate()` at an auth-state
  change (login, logout, privilege elevation) to invalidate a pre-auth id an
  attacker may have planted.
- **Request-body cap** (413) + a capped JSON decode (400 on malformed/oversized
  input; unknown signal keys and trailing bytes after the JSON value are
  ignored, not rejected — it is not a strict decode), and a **panic recover**.
- **`nosniff` + a hash-admitted CSP** on the page and patch responses. The CSP
  includes `'unsafe-eval'` because Datastar compiles `data-*` expressions with
  the `Function` constructor — without it every action is silently dead in the
  browser. The one inline script via emits (the reconnect manager) is
  library-controlled, so it's admitted by its own SHA-256 hash — no nonce, no
  per-key state, nothing for an injected `<script>` to borrow. Honest posture:
  the CSP is a seatbelt against *injected* inline script; the load-bearing
  defenses are output escaping, the attribute-name allowlist, and the same URL
  gate on every `Redirect` target (`javascript:`/`data:`/`//` are dropped
  loudly: a Datastar action falls back to its normal element-patch response, a
  native `PostForm` submit falls back to a full-page re-render, and an unsafe
  target from `OnInit` answers 500).
- **HTML/attribute escaping** with an attribute-name allowlist (`h.RawAttr` /
  `h.Data` and the typed helpers reject injectable names).

## Status

Built and tested — `-race`-clean, adversarially reviewed, eight runnable
examples, the whole live stack verified in real headless browsers
(`vtbrowser/`, `-tags browser`):

- **Hardened stateless core** (`example/counter`): by-value `Register`, origin
  floor, hash-admitted CSP, body cap, panic-recover, compile-time `View`
  constraint, attribute-name allowlist. An action's response self-classifies —
  element-patch when the render changed, `204` when it didn't.
- **Reactive handles** (`example/greeting`): client-resident `Signal[T]` with
  handle-identity wire names — `Bind()` and `Display()` share one name, so the
  greeting updates live as you type, entirely client-side; `When`/`Each`
  render conditionals and lists.
- **Live islands + `State[T]`** (`example/pulse`): render a `State[T]` or
  register a `Tick` and a composition becomes a live island with a per-tab SSE
  stream; `State[T]` is
  server-authoritative, read from the pure View and element-patched on change,
  `Tick` drives the push. `Tick`/`Listen`/action handlers all run on the
  connection's one goroutine — a handler that blocks (I/O, an unbounded
  loop) stalls every other tick, action, and push on that same connection,
  and delays that connection's shutdown until it returns.
- **Interactive live actions** (`example/chat`): a live-island action routes —
  via the `via_tab` handshake (an unguessable per-connection id echoed in the
  `X-Via-Tab` header) — to *this* connection's island, mutates its state, and the
  result is pushed over its SSE. The element push omits `data-signals`, and
  deliberate signal changes ride a signal-patch, so a fan-out never clobbers what
  a user is typing.
- **Multi-user fan-out** (`example/feed`, `example/chat`): an in-process
  `via/topic.Topic[T]` broker + `ctx.Listen` / `ctx.OnDispose` — one publish
  fans out to every connected island.
- **Sessions** (always available): `ctx.Session().Put[T]`/`Get[T]`/`Clear[T]`,
  a typed per-browser store keyed by Go type (no tags, no reflection — a
  typed-nil sentinel), behind a signed-HMAC cookie issued lazily on the first
  write — apps that never store anything stay cookieless. Sessions do not
  rotate their id on their own: call `Session.Rotate` at an auth-state change
  (login, logout, privilege elevation) to invalidate a session id an attacker
  may have planted beforehand (fixation defense). Idle sessions expire lazily
  on access (past the TTL, the next read/write treats them as gone) — there
  is no background sweep, so a
  session that is never touched again is not proactively evicted. A session
  that idles past its TTL while a live stream is still open (the stream
  itself does nothing to keep it warm) turns every later dispatch on that tab
  into a 403 "session mismatch" until the page is reloaded. The signing
  key resolves
  `WithSessionKey` → `VIA_SESSION_KEY` env → a random per-process key (warned on
  first use — set a stable key so sessions survive restarts and span pods).
  `WithSessionTTL`/`WithSessionCookieName` tune it. The cookie is `Secure`
  automatically over TLS (so `http://localhost` dev still works);
  `WithSecureCookies` forces it on behind a TLS-terminating proxy.
- **Resilience floor + reconnect**: a server-side keepalive comment frame
  (fixed 25s) and a per-frame write deadline (fixed 10s) ride the island's
  single goroutine; a failed frame write tears the
  island down (runs disposers, stops ticks) so a half-open peer — gone without a
  FIN — can't leak its goroutine and timers. A client reconnect manager surfaces
  a "Reconnecting…" banner on a dropped stream and reloads to re-bootstrap when
  Datastar gives up.
- **Live-island multiplexing** (`example/dashboard`): embed sub-compositions as
  plain struct fields — `via.Embed(p.Clock)` in the parent's `View`. Each child
  gets its own `OnInit`; a child that neither ticks nor holds `State` is a plain
  in-place component, one that does is a live island, and all the live children
  on a page share the tab's *one* SSE stream on one goroutine — each re-renders and patches only its own region
  (`#via-i{n}`, pushed in place — its container is never morphed by a
  parent's patch), its actions route by island id + the tab handshake, and its
  signals are slot-scoped so siblings never collide. The parent's literal seeds a
  child's dependencies (a shared `*Topic`, a store) at registration; generic
  layouts (`Shell[C]{Body C}`) compose one shell with any page. Ownership is
  by value: the field literal seeds the child, each connection gets its own
  copy (value state stays per-tab), and pointer deps are the deliberate
  sharing channel. **Known limitation:** a live island cannot itself embed a
  further live island — nesting is one level deep (the root, or a live child
  directly under a plain root, or through further plain `via.Embed`s); it
  panics at render, loud and early, rather than misroute an action. Plain
  (non-live) composition still nests to any depth. Nested live composition
  (a dynamic set of live children addressed by identity) is a deferred
  feature. `State` is per connection: a native `PostForm` submit is a
  navigation, opens a new connection, and reseeds it — persist through the
  session or a shared pointer dep, or `Redirect` instead of returning a page.
  An `Embed`'s position among the page's `Embed` calls is its identity
  (container id, signal prefix, dispatch address); it must be the same on
  every render for the life of a connection — a `When` around an `Embed`
  must depend only on data fixed by `OnInit` or the field literal, never on
  time, a client signal, or shared state that changes while the page is
  open.
- **Per-row list actions** (`example/poll`): a row's button carries the row's own
  datum — `via.OnArg("click", l.Delete, item.ID)` — and the handler receives it as a
  typed parameter, `func(*via.Ctx, int)`. Identity rides with the click, so a list
  that grows, shrinks, and **reorders** never misroutes: the value (not the
  positional slot) picks the row. Still a named method value — no `&`, no closure.

- **Multi-page apps + auth + uploads** (`example/forum`): `via.NewRouter()` with
  `r.Mount("/path", Page{})` serves a whole app behind one
  handler, each page's actions namespaced under its mount. `OnInit(*Ctx) error`
  is the per-request hook that loads session/path data into a stateless page
  before its ctx-free `View` — return `via.ErrNotFound` for a vanished record
  (404); any other error answers 500, and the View never renders a lie. A
  session check + `ctx.Redirect("/login")` inside `OnInit` is the whole
  protected-page story — no separate guard mechanism. `via.PostForm(handler, …)` renders a **native**, always-multipart form whose
  submit runs server-side and `ctx.Redirect("/…")` issues a 303 — the
  server-rendered auth flow the bundled Datastar can't do.
  The same form handles the avatar upload: a file `<input>` just works, read
  with stdlib's `ctx.Request().FormFile("avatar")`.
  `ctx.Param[int]("id")` reads the named `{id}` segment of `"/thread/{id}"`.
  The forum proves these compose into a full multi-page app.

**The flagship is `example/chat`** — a live, multi-user chat room with a presence
count, in ~60 lines that read like a static page. Two-browser-verified: a message
typed in one tab appears in the other, the "N online" header tracks connections,
and the composer clears on send without clobbering a concurrent draft.

**Restarts and deploys.** Sessions derive from the signing key, so with a
stable key (`WithSessionKey` / `VIA_SESSION_KEY`) a restart or a rolling
deploy keeps cookies valid across pods. The CSP is a pure function of the
Head, so pods with different keys still serve identical policies. Live-island
state is in-memory and per-connection: a deploy drops
the stream, the client reconnect manager shows "Reconnecting…" and reloads to
re-bootstrap — the page comes back from server truth, not from replayed frames.
Error pages are plain `http.Error` text for now (404 for `via.ErrNotFound` /
a decode-miss `Param`, 500 for the rest); a `WithErrorPage` hook is post-1.0.
An action URL carries its unit's shape digest as `?v=` and answers `410 Gone`
— on both the stateless and live dispatch paths — for a stale digest (the
`View()` shape changed since the client rendered), an out-of-range action
index, an unknown island, or a live action with no connection for its tab.
Datastar resolves a non-2xx response silently and moves on; a native `<form>`
submit shows the browser's own error page.

Deferred (correctly out of 1.0 scope): a keyed cursor for the narrow remaining
dynamic-shape cases — per-row *signals/inputs* in a **reordering** list, and
lists *of* live islands (per-row actions are done via `OnArg`; fixed
embeds via `via.Embed` are done); nested live composition (a live island
embedding a further live island, or an embedded live island's own `View`
calling `Embed` at all — v0.8 refuses both at render instead); and
at-least-once redelivery (a push onto
a dropping socket fails the write and tears down rather than being buffered
for replay). The SSE GET stream applies the same origin floor as the action
POST and is capped at a fixed number of concurrent connections (10,000; over
the cap returns 503) — router-wide, not per IP: an anonymous client can open
enough connections on its own to fill the cap and 503 everyone else. A per-IP
cap is deferred past v0.8.

**Reconnect.** A dropped stream and its reconnect build an entirely new
`liveConn`: any action POST still in flight against the old tab id answers 410
once the old connection is gone, and the client's own reconnect manager is
what re-bootstraps the page from server truth (also see "Live-island
multiplexing" above for the one-level-deep nesting limit).

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
