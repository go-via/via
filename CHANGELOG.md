# Changelog

## v0.8.0 — the v2 core goes mainline

The rebuilt "bare core" replaces the v1 tree. Module path is now
`github.com/go-via/via` (no `/v2` suffix); v1 history is merged, the tree is
the v2 core. **Requires Go 1.27.**

### Security defaults changed

Read this even if you read nothing else. Two defaults moved in the permissive
direction relative to v1, deliberately, and neither announces itself at
runtime unless you look:

- **The origin floor is OPEN by default.** v1 enforced; v0.8 accepts an action
  from any origin until `WithTrustedOrigin` names one, at which point
  enforcement switches on for the whole endpoint. The reasoning: the per-tab id
  is the CSRF token and does the load-bearing work, and local development over
  plain http has to work with no configuration. The consequence: **a production
  deployment that never calls `WithTrustedOrigin` is running with cross-origin
  enforcement off.** The option name says what it allows, not that it also flips
  enforcement, so via now logs one line at startup when the floor is open. Set
  the option in production.
- **Sessions are always on**, lazily — the cookie is issued on first write. If
  no key is configured, via mints a random per-process one and warns once:
  sessions then do not survive a restart or span pods. Set `WithSessionKey` or
  `VIA_SESSION_KEY`.

`WithInsecureOrigin` is gone, because there is no longer a secure default to
opt out of.

### Breaking

v0.8 is a rebuild, not an incremental release: the v1 surface (plugins,
`h.Group`/`h.If` helpers, theme options, `WithoutSSEReconnect`, the old
composition types) is replaced wholesale by the core below. Treat migration
as a re-read of the README, not a diff.

- **Requires Go 1.27.**
- **`h.SafeURL` is gone.** The URL policy — http/https/relative admitted,
  `javascript:`/`data:`/protocol-relative refused — moved to
  `internal/hcore`, where `h`'s typed attributes and via's `Redirect` gate
  share one implementation. It was exported only to cross a package boundary,
  and it carried a second copy of the three checks: two gates that agreed
  today is how one of them later admits a `javascript:` target the other
  refuses. Nothing outside via needed it; the typed `h.Href`/`h.Src`/
  `h.Action` attributes and `via.Redirect` enforce the policy for you.
- **`via/sess` merged into the root package**: `Session` is a real type with
  `Put`/`Get[T]`/`Clear[T]`/`Rotate` methods, reached via `ctx.Session()`; the
  `sess` subpackage and its `internal/sessbridge` shim are gone.
- **Bare mutators**: `Signal.Set(v)`, `State.Set(v)`, `List.Append(v)` — no
  `ctx` argument. State is bare; ctx is for the request.
- **Composition is `via.Embed`**: child compositions are plain struct fields
  rendered with `via.Embed(p.Field)`. `Slot`, `Child[C]`, `NewChild`, `Fill`
  and the `.Embed` method are gone. Generic layouts: `Shell[C]{Body C}`.
- **`via.Mount(r, …)` is `r.Mount(…)`**: the router owns its mounts.
- **`via.Param[T](ctx, n)` is `ctx.Param[T](n)`; `via.Redirect(ctx, path)` is
  `ctx.Redirect(path)`**: request-scoped verbs are Ctx methods.
- **`via.Listen` is a Ctx method**: `ctx.Listen(topic, handler)`.
- **`OnInit(*Ctx) error`**: the per-request hook now returns an error —
  `via.ErrNotFound` answers 404, anything else 500; the View never renders a
  lie. The same sentinel works from `OnConnect`.
- **Sessions are always on** (lazily — the cookie is only issued on the first
  write); the session options are tune-only. Key resolution:
  `WithSessionKey` → `VIA_SESSION_KEY` → random per-process key (warned at
  first mint).
- **Origin floor is open by default**; `WithTrustedOrigin` turns enforcement
  on (`WithInsecureOrigin` removed). The per-tab id remains the CSRF token.
- **`h` is elements + attributes + `Str` only**: the render plumbing
  (`Dyn`/`DynAttr`/`NewRenderer`/`Renderer`/`Binder`) moved behind
  `internal/hcore`.
- **One doorway each**: conditionals are `via.When` (no `If`), growing lists
  are `via.List[E]` (`State[[]E]` + `Append`), session rotation is
  `ctx.Session().Rotate()`. Themes are your CSS, not options; the SSE reconnect
  manager is always on.

### Added

- **A plain (non-live) page may embed live islands** as sibling struct
  fields — each streams and patches independently over the page's one
  connection. **Known limitation:** a live island cannot itself embed
  another live island, and an embedded live island's own `View` cannot call
  `via.Embed` at all — either panics at render, loud and early. Nested live
  composition is deferred; plain (non-live) composition still nests to any
  depth.
- **Typed attribute helpers in `h`**: `ID`, `Class`, `Style`, `Type`, `Name`,
  `Value`, `Placeholder`, `Title`, `Alt`, `Rel`, `For`, `Target`, `Lang`,
  `Role`, `Method`, `Enctype`, `Accept`, `AutoComplete`, `Width`, `Height`,
  `Min`, `Max`, `Step`, `Rows`, `Cols`, `MaxLength`, `MinLength`, `Colspan`,
  `Rowspan`, `TabIndex`, plus the boolean attributes `Disabled`, `Checked`,
  `Required`, `ReadOnly`, `Selected`, `Multiple`, `AutoFocus`, `Hidden`,
  `Open`, `NoValidate`, `Async`, `Defer`, `Inert`, `Loop`, `Muted`,
  `Controls`, `PlaysInline`, `Reversed`. `h.Placeholder("x")` cannot be
  misspelled the way `h.RawAttr("placholder", "x")` can. The booleans are the
  substantive part: an HTML boolean attribute is *present or absent*, and
  `disabled="false"` disables a control in every browser, so the off state now
  emits no attribute at all — something `RawAttr` could not express.
  `RawAttr` and `Data` stay for everything else.

- **`List[E]` gets `Remove`** alongside `Append`: it panics on an out-of-range
  index rather than silently doing nothing. Rows that can be removed or
  reordered need a stable `id` so the morph matches by identity, not position.
- **`List.Each(row)`**: sugar over `via.Each(l.Get(), row)`.
- **Full HTML5 vocabulary in `h`** (~105 constructors), minus the page-shell
  and footgun tags (`html`, `head`, `script`, `template`, …) — those stay
  via's. Typed `h.Href`/`h.Src`/`h.Action` attributes gate their URL through
  a `javascript:`/`data:` allowlist and neutralize to `#` loudly.
- **Router**: `via.NewRouter` + `r.Mount("/path", Page{}, guards...)`
  serves a multi-page app behind one handler; `via.Register` is now literally
  `Mount` at `/` — one dispatch pipeline. Mounted pages carry the full live
  stack (SSE, live actions, islands). Every action — a `@post` event
  binding, a native `PostForm` submit, or a live unit's — posts through one
  `dispatch`/`respond` pair to `/_via/a/{island}/{n}` (the root is island 0);
  the response mode (element-patch vs a native form's full-page re-render) is
  read off the request's `Datastar-Request` header, not the route. `/_via/f/`
  is gone.
- **Path params**: `Mount("/thread/{id}", …)` + `ctx.Param[T]("id")` — Go's
  own `http.ServeMux` syntax, named not positional; a segment that doesn't
  decode is an honest 404 on every stateless transport, and naming a segment
  the mount pattern doesn't have panics at request time (a wiring mistake).
- **`ctx.Listen[T](topic, handler)`**: subscribe + pump + auto-dispose
  in one line.
- **Arg events**: `via.OnClickArg` carries a typed render-time datum with the
  event — a per-row action without an `&` at the call site.
- **Native forms**: `via.PostForm` (server-side submit + 303). Always
  multipart, so a file `<input>` just works — read it with stdlib's
  `ctx.Request().FormFile(name)`; no separate upload verb or type.
- **Redirect from a `@post` action**: `via.Redirect` ships a constant
  `location.assign()` script that every document this app serves admits by
  its sha256 — no shared key, so pods agree even with different keys. The
  target rides as a `data-via-to` attribute Datastar copies onto the script.
  Targets are gated by the shared URL policy; unsafe ones are dropped loudly with an
  element-patch fallback. Browser-verified under the strict CSP.
- **`WithDocumentHead(via.Head{...})`**: the document shell — title, lang,
  meta, links, external scripts, one inline style, extra font origins. A typed
  struct rather than an `h.H`, because the head is also the app's origin
  declaration: `script-src`, `style-src` and `font-src` are derived from it,
  so a declared host works under the strict CSP and an undeclared one stays
  blocked. `InlineStyle` is admitted by its own sha256. Malformed heads panic
  at `Register`; the zero `Head` serves what via served without the option.
- **Resilience floor**: SSE keepalive comment frames (`WithSSEHeartbeat`),
  per-frame write deadlines (`WithSSEWriteTimeout`), half-open teardown, a
  client reconnect manager with a "Reconnecting…" banner and a capped
  reload-to-re-bootstrap (2), `WithMaxSSEConnections` (503 over the cap).
- **`vt.App.Client()`**: the harness's `*http.Client`, wired to reach its
  in-memory server. A test that hand-rolls a request past the `Get`/`Action`/
  `Connect` builders must send it through this, not `http.DefaultClient` —
  `http.DefaultClient` has no route to an in-memory listener.
- **`vt.Conn.Peek()`**: returns the next buffered SSE frame without blocking,
  for asserting something has *not* arrived yet without advancing time
  further to find out.

### Changed

- **Live actions run synchronously** on the connection's own goroutine: the
  POST that triggers one now waits for it to finish (the shape a stateless
  action already had), so it can set the session cookie and answer a
  `ctx.Redirect` on the same response instead of firing-and-forgetting into
  the next SSE push. A slow handler slows its own click; nothing else on the
  connection.
- **`vt` runs the live runtime under `testing/synctest` on an in-memory
  network** (`httptest.NewTestServer`): the 25s keepalive and 10s write
  deadline are exercised at their real production values in milliseconds of
  wall time, and a leaked island goroutine now fails the test instead of
  passing silently. Gotcha for anyone writing `vt` tests: the request `Host`
  is fixed at `"example.com"` (httptest's in-memory server address), not
  `localhost`, and any request built by hand rather than through `vt`'s own
  builders must go through `App.Client()` — `http.DefaultClient` cannot dial
  the in-memory listener at all.
- **A live action no longer re-renders before it runs.** It hydrates and
  dispatches against the actions/hydrators table the previous push already
  built, then pushes once — one render per action, not two. An action id
  outside that table (a click racing a push, or a branched `View` that
  shifted it) now answers `410` instead of silently doing nothing.
- **Every action URL carries a `?v=` shape digest** (`/_via/a/{island}/{n}?v={digest}`),
  a short hash of that render's signal order, action count, and embedded
  islands' layout. `dispatch` recomputes it fresh and 410s on any mismatch —
  the one check that replaces the old signals-only, root-only render-shape
  guard and the per-path index-range checks. This is what closes the real
  misroute hazard: a `View` that branches on server state shifts every later
  action's index, and a stale click landing on the wrong handler used to be a
  silent misroute (or, on the live path, a silent no-op) rather than a `410`.
  **Known limitation:** a live unit whose `View()` shape depends on a
  background tick (not just the triggering action) can shift its own digest
  out from under a client that already rendered — in an adversarial probe
  this 410s on the order of a fifth of clicks. Datastar resolves a non-200
  response silently with no visible error; on a live page the next push
  carries the new digest and heals it, but on a stateless page the button
  stays dead until reload. Keep a live unit's action/signal/embed layout
  stable across ticks if clicks must never 410.

### Fixed

- **A native `PostForm` submit inside a live unit now runs its handler and
  answers with a full-page re-render.** A real browser form submit can't set
  the `X-Via-Tab` header (that's fetch-only), so it used to miss the
  connection entirely and 410 ("no live connection for this tab"), or — if
  the header were somehow present — 500 on a live dispatch's `nil`
  native-render path. `PostForm` now also renders a hidden `_viatab` field,
  kept in sync with the `$_viatab` signal, that dispatch reads as a fallback
  when the header is absent; it is checked against the same per-mount
  ownership the header gets.
- **An action POST against a live unit no longer waits on that unit's own
  SSE push.** A stalled reader could block the write behind an action for up
  to the write timeout — or forever, since `WithSSEWriteTimeout` could be
  disabled — while the connection's single goroutine, and every net/http
  goroutine dispatched behind it, sat parked. The action now answers as soon
  as it has run; the push ships as a later, independent unit on the same
  connection. **`WithSSEWriteTimeout` now panics on a non-positive duration**
  instead of disabling the deadline — the deadline is what bounds this wait,
  so it can no longer be turned off.
- Signal warnings: `Set` on a signal the View never rendered warns once
  instead of silently doing nothing.
- Session-cookie signature mismatch (two apps clobbering one cookie name)
  logs one loud diagnostic instead of silently resetting sessions.
- Redirect godoc claimed a CSP nonce; the script is hash-admitted and works
  without a session.
- **A panicking `View()` now recovers to a 500** instead of aborting the
  transport — previously only actions recovered, so a panic on the GET/render
  path tore down the connection with no response at all. `vt`'s
  `abortOnPanic` knob is gone; the transport-abort case it existed to test no
  longer occurs.
- **`ctx.Redirect` now navigates from a live action and a stateless embedded
  island's action** — both used to drop it silently (fire-and-forget with no
  open response to carry it on).
- **A stateless embedded island's `Signal.Set` now reaches the client.** The
  island-action response rebuilt the container with no `data-signals`
  attribute at all, so a Set that changed only a bound (not displayed) signal
  vanished — server memory updated, browser never told.
- **Guards and `OnInit` now run on an embedded island's action**, not only
  the page GET and the root's own action — the island-action route used to
  bypass both entirely.
- **A stateless island's action re-render under a parametrised mount
  (`r.Mount("/thread/{}", …)`) now carries the concrete path**, not an empty
  action base — an embedded island never inherited its parent's mount prefix
  at all.
- **A live push under a parametrised mount now carries the concrete path**,
  not the literal `{p0}` pattern wildcard — the SSE connect closed over the
  mount's pattern instead of resolving it per connection.
- **`RequireSession` (and every guard) now protects a mounted page's SSE
  stream, and its `OnInit` now runs before the connect render** — the stream
  route ran neither, so a guarded live page's push channel was reachable by
  anyone who knew the URL.

Earlier releases (v0.7.0 and back) predate this changelog; see the git tags.
