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
  `javascript:`/`data:`/protocol-relative refused, including a leading `\` or
  `/\` (WHATWG parsing treats `\` as `/`, so `/\evil.com` is protocol-relative
  too) — moved to `internal/hcore`, where `h`'s typed attributes and via's
  `Redirect` gate share one implementation. It was exported only to cross a
  package boundary, and it carried a second copy of the three checks: two
  gates that agreed today is how one of them later admits a `javascript:`
  target the other refuses. Nothing outside via needed it; the typed
  `h.Href`/`h.Src`/`h.Action` attributes and `via.Redirect` enforce the
  policy for you.
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
- **Router**: `via.NewRouter` + `r.Mount("/path", Page{})`
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
- **`ctx.Redirect`**: a PostForm/OnInit facility (303). Targets are gated by
  the shared URL policy; unsafe ones are dropped loudly with an element-patch
  fallback. A Redirect queued from a Datastar `@post` action cannot navigate
  the page — it is logged and dropped; use a `PostForm` handler or an
  `<a href>` instead.
- **`WithDocumentHead(via.Head{...})`**: the document shell — `Title`, `Lang`,
  `Raw` head markup (emitted verbatim after via's own `<meta charset>`), one
  inline style, and `ScriptOrigins`/`StyleOrigins`/`FontOrigins` — the app's
  own origin declaration for whatever `Raw` references, one list per
  directive so a stylesheet CDN isn't also script-trusted. `script-src`,
  `style-src` and `font-src` are derived from those lists, so a declared host
  works under the strict CSP and an undeclared one stays blocked.
  `InlineStyle` is admitted by its own sha256. Malformed heads panic at
  `Register`; the zero `Head` serves what via served without the option.
- **Resilience floor**: SSE keepalive comment frames (fixed 25s), per-frame
  write deadlines (fixed 10s), half-open teardown, a client reconnect manager
  with a "Reconnecting…" banner and a capped reload-to-re-bootstrap (2), a
  fixed 10,000-connection cap (503 over it).
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

- **A live island's `Embed`ded child is looked up by the right address.**
  `embedViewer` indexed a connection's units by the child's plain island
  index, but `liveConn.replace` keys them by `islandIdx+1` (root is 0) — a
  live root that embeds anything re-entered `Embed` on its own render and
  panicked on the first push; a plain root with one live island reseeded a
  by-value copy instead of the connected instance on a native re-render; two
  live islands collided on the same address, duplicating one and losing the
  other.
- **A live action abandoned mid-flight (the POST's context gave up while its
  mutation was still queued behind the island goroutine) no longer runs
  against a dead `ResponseWriter`.** The mutation used to apply anyway (after
  the client had already been answered 410), racing `SetCookie`/`Header()`
  against the server's own post-handler teardown; it's now skipped entirely
  once the request is known abandoned.
- **A live action no longer rewrites the Ctx a Tick or Listen handler
  holds; those keep the connect request.** Dispatch used to write the
  action's own req/sessW/redirect directly onto the render-time Ctx an
  island's Tick/Listen closures capture for the life of the connection, so
  firing one action left every later timer or subscription callback reading
  that action's request instead of the connection's. Each action now runs
  against a fresh, per-dispatch Ctx instead.
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
  to the (fixed, non-disableable) write timeout, while the connection's
  single goroutine, and every net/http goroutine dispatched behind it, sat
  parked. The action now answers as soon as it has run; the push ships as a
  later, independent unit on the same connection.
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
- **`OnInit` now runs on an embedded island's action**, not only the page GET
  and the root's own action — the island-action route used to bypass it
  entirely.
- **A stateless island's action re-render under a parametrised mount
  (`r.Mount("/thread/{}", …)`) now carries the concrete path**, not an empty
  action base — an embedded island never inherited its parent's mount prefix
  at all.
- **A live push under a parametrised mount now carries the concrete path**,
  not the literal `{p0}` pattern wildcard — the SSE connect closed over the
  mount's pattern instead of resolving it per connection.
- **`OnInit` now runs before the connect render, and its Redirect is
  honoured, on a mounted page's SSE stream too** — the stream route ran
  neither, so a session-gated live page's push channel was reachable by
  anyone who knew the URL.
- **A panicking render inside a dispatched action, a Tick, or a Listen
  handler no longer kills the live stream.** Only the action's own mutation
  was recovered; a panic in the re-render it (or a timer, or a topic fan-out)
  triggers unwound the whole connection goroutine — after an action had
  already answered 204. Each pulse item now recovers on its own; the
  connection stays up and keeps serving later actions/ticks.
- A value-carrying action (`OnClickArg`) with a malformed, empty, or `null`
  `?a=` now answers 400 instead of silently handing the handler a zero value
  (e.g. deleting row 0).
- **A panic in a native `PostForm`'s post-mutation re-render — run on the
  island goroutine after the mutation already answered — now answers 500
  instead of hanging the POST forever;** the SSE keepalive beat is recovered
  the same way.
- **A live connection opened under a session now requires that same session
  on every dispatch against it.** The tab id used to be the sole credential —
  with the origin floor open (the default), a leaked tab id let a request
  carrying no session cookie at all, from any origin, drive that connection's
  actions. Bound by the session's `*sessionData` pointer, not its id, so a
  `Rotate` after connect does not break the binding. **Extended:** a
  connection that started anonymous and only logs in afterward — a live
  action calling `Session().Put`/`Rotate`, or the recommended
  "establish the session in `OnConnect`" pattern — now binds too, on that
  first mint; previously only a session that already existed at connect time
  was covered, so a tab that logged in mid-connection kept accepting a
  cookieless dispatch until reload.
- **A panicking `OnDispose` function no longer skips every disposer
  registered after it.** Each disposer now runs through the same per-item
  recover a Tick/Listen/action pulse already gets; a skipped disposer (e.g.
  `sub.Stop`) used to leak its subscription for the life of the process.
- `Tick`/`Listen` called after `OnConnect` has returned now log loudly
  instead of silently registering nothing.
- A panic before a live stream's headers are sent now answers 500 instead of
  falling through to Go's default 200-with-empty-body.
- `WithSessionKey`/`VIA_SESSION_KEY` under 16 bytes now panics at
  construction instead of silently signing cookies with a guessable key.

### Known limitations

- A live connection's tab id is a bearer credential for that connection's
  actions until it binds to a session (at connect, or on first login
  afterward); an anonymous connection has no session to check against at
  all, so never render, log, or leak a tab id outside its own client.
- There is no per-IP or per-tab cap on concurrent SSE connections beyond the
  router-wide `WithMaxLiveConnections`; a single client can still open many.
- Action-body JSON decoding is not strict: unknown signal keys and trailing
  bytes after the JSON value are ignored, not rejected.
- Session idle-TTL eviction is lazy — enforced on the next access, not swept
  proactively — so a session nobody ever touches again outlives its TTL in
  memory.
- A `Tick` handler that blocks (I/O, an unbounded loop) pins the connection's
  one goroutine, which defeats a clean shutdown of that connection until the
  handler returns.
- A session that idles past its TTL while a live stream is open turns every
  later dispatch on that tab into a 403 "session mismatch" until the page is
  reloaded — the stream itself does not keep the session warm.
- A live connection only binds to a session an action actually MINTS
  (`Session().Put`/`.Rotate`) while none existed at the start of that
  request; a merely read-only `Session().Get` never binds it, even one
  carrying a foreign valid cookie, closing the capture window an earlier
  fix left open. Until an action mints one, the tab id above remains the
  connection's only credential.
- A live action racing a concurrent `Session.Rotate` on the same
  connection can answer one spurious 403 "session mismatch" — the action
  ran against the id the cookie held a moment before Rotate moved it. A
  retry with the refreshed cookie succeeds. The same class of thing
  happens if a cookieless action is queued behind a login action on the
  same connection (a double-click during login): before the connection
  binds it would have applied, but now it answers 403 "session mismatch"
  once, because it is indistinguishable from an attacker's request at
  that point. This is deliberate, not a bug, but it is a behaviour change.
- A session minted from a `Tick`/`Listen` handler (as opposed to an action
  or `OnConnect`) has no open response to carry a cookie, so it is created
  and then orphaned until its TTL. In some configurations this is silent:
  if the connection's `OnConnect` ever calls `Session()` at all — including
  a read-only `Get`, the pattern this doc recommends — the resulting
  handle is cached with the connect response already attached, so a later
  `Tick`/`Listen` `Put` writes its `Set-Cookie` onto that dead response
  with no warning logged. Establish sessions in `OnConnect` or an action
  instead.

Earlier releases (v0.7.0 and back) predate this changelog; see the git tags.
