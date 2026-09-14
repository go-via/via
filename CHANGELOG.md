# Changelog

## v0.8.0 — the v2 core goes mainline

v0.8 is a rebuild. The v1 tree is replaced by a smaller core with no plugins,
no subpackages beyond `h` and `topic`, and no configuration knob that a
constant could serve instead. Module path is now
`github.com/go-via/via` (no `/v2` suffix); v1 history is merged, the tree is
the v2 core. **Requires Go 1.27.**

### New

- **`Router.Close`.** There was no shutdown path at all: every stream
  goroutine, its `Tick` timers and its `Listen` subscriptions hung off
  `req.Context()`, which `http.Server.Shutdown` does not cancel — so Shutdown
  blocked on every open tab until its own deadline and then killed them
  mid-frame. `Close` cancels a router-level context each stream derives from
  and returns once the last one is gone: streams end with a clean end of
  response rather than a truncated one, every `OnDispose` runs, an action POST
  against a closing tab answers `410`, and a connect arriving after `Close` is
  refused `503`. Call it before `http.Server.Shutdown`; it is safe to call
  more than once.

- **`StateOf` / `ListOf`.** `State[T]` holds its value in an unexported field,
  so a parent could not seed an embedded child's state from a composite
  literal — which is exactly what `Embed` asks you to do. `via.StateOf("lobby")`
  and `via.ListOf("a", "b")` are the constructors.

- **`Ctx.Context`** returns the context bounding this unit's work — the STREAM
  context on a live unit, so a `Tick` or `Listen` handler can abandon a slow
  call when the tab disconnects or the router closes. `Ctx.Request`'s context
  cannot serve: a live action runs after its POST has acked.

- **`WithSessionStoreTimeout`** caps one session store round-trip (default 5s).
  Store calls deliberately outlive the request's context, so without it a hung
  backend pinned the request goroutine indefinitely. A read-modify-write with
  CAS retries is bounded as a whole, not per attempt.

- **The goroutine model is now stated in the package doc.** `Signal`, `State`,
  `List`, `Ctx` and `Session` take no lock and are correct only because every
  callback against one instance runs on one goroutine; each type now says so,
  and the package doc says which goroutine and what to do instead (publish to
  a `Topic` the unit `Listen`s to).

- **`SessionStore` + `WithSessionStore`.** Session data was a per-Router map in
  process memory: every deploy logged every user out and multi-pod was
  impossible. `SessionStore` is a three-method blob map (`Load`/`Save`/`Delete`,
  each taking a `context.Context`) that a user implements against Redis or SQL
  without reading any of via's internals. Rotation is Save-then-Delete and
  expiry is the `ttl` handed to Save, so no implementation reimplements either;
  via also stamps its own deadline into the blob, so a backend with no TTL
  support stays correct. **Breaking:** session values now round-trip through
  `encoding/json`, keyed by the Go type's printed name, so a `Session.Put`
  value must be JSON-encodable (it panics if not) and a value written under a
  type that has since been renamed reads back as absent.

- **`Reloader`**: `OnReload(*via.Ctx) error`, run after an action and before the
  response render, on the plain path and the live path alike. It fixes the
  commonest week-one defect: `OnInit` loads, the handler mutates the store, and
  the render still shows what `OnInit` read, so the action answers 204 and the
  UI never moves. `OnReload` is a second hook rather than a second `OnInit` run
  because `OnInit` is an initializer: it mints and defaults the session, seeds
  signals from the request URL, registers `Tick`/`Listen`, and may `Redirect`
  or return `ErrNotFound`, none of which is safe to repeat once a handler has
  committed a mutation. `Tick`/`Listen` are no-ops inside `OnReload` (liveness
  stays the GET/connect verdict), and it is skipped when the handler queued a
  `Redirect`. A unit that declares NEITHER hook and answers 204 now logs one
  line naming `OnReload`, so the failure is never silent again.

- **Mount and Embed check the lifecycle hooks.** `Initer`/`Reloader` are
  duck-typed, so a typo or a signature change unhooks a composition silently.
  A method literally named `OnInit`/`OnReload` whose signature is not
  `func(*via.Ctx) error` now panics at Mount/Embed, and a method that *does*
  have that signature under a near-miss name (`Reload`, `OnInitialize`,
  `Refresh`, …) on a type implementing neither interface logs one line naming
  the assertion that would have caught it. The assertion itself —
  `var _ via.Initer = (*Front)(nil)` — is the only airtight form and is now in
  every example.

### Security defaults changed

- **A mount parameter can no longer break out of a Datastar expression.** A
  `{slug}` segment was concatenated raw into `data-init="@post('…')"` and into
  every action/form URL; Datastar evaluates those attributes as JavaScript
  under a CSP that must carry `'unsafe-eval'`, so a quote in the segment ran
  attacker script on any mount whose `OnInit` did not coerce it. The segment is
  now path-escaped where the base is built AND HTML-escaped where the attribute
  is written. `Param[T]` still sees the decoded value.

Read this even if you read nothing else. Two defaults moved in the permissive
direction relative to v1, deliberately, and neither announces itself at
runtime unless you look:

- **Origin enforcement (the "origin floor": the check on every state-changing
  request that its `Origin`/`Sec-Fetch-Site` names a host you trust) is OPEN by
  default.** v1 enforced; v0.8 accepts an action
  from any origin until `WithTrustedOrigin` names one, at which point
  enforcement switches on for the whole endpoint. The reasoning: on a LIVE page
  the per-tab id is a synchronizer token and does the load-bearing work, and
  local development over plain http has to work with no configuration. The
  limit of that reasoning: a PLAIN page has no connection and no tab id
  (`viatab` and `_viatab` are empty), so a cross-origin `PostForm` submit is
  accepted with the floor open, and what defends it is the session cookie's
  `SameSite=Lax`, and the request arrives unauthenticated. The consequence:
  **a production deployment that never calls `WithTrustedOrigin` is running
  with cross-origin enforcement off.** The option name describes what it allows
  and says nothing about it also flipping enforcement, so via now logs one line at startup when the floor is open. Set
  the option in production.
- **Sessions are always on**, lazily: the cookie is issued on first write. If
  no key is configured, via mints a random per-process one and warns once. The
  key signs the COOKIE only; the DATA lives behind the new `SessionStore`
  interface, whose default is a map in this process's memory. Surviving a
  restart or spanning pods takes both `WithSessionKey`/`VIA_SESSION_KEY` and
  `WithSessionStore`; via warns once at the first session mint when the store
  is the process-local default.

`WithInsecureOrigin` is gone, because there is no longer a secure default to
opt out of.

### Breaking

v0.8 is a rebuild rather than an incremental release: the v1 surface (plugins,
`h.Group`/`h.If` helpers, theme options, `WithoutSSEReconnect`, the old
composition types) is replaced wholesale by the core below. Treat migration
as a re-read of the README rather than a diff.

- **Requires Go 1.27.**
- **`NewMemorySessionStore` returns `*MemorySessionStore`, not `SessionStore`.**
  The store implements `VersionedSessionStore` (the CAS path a merge needs),
  and returning the narrow interface erased that at the type level, so any
  wrapper built around the return value silently dropped to the lossy merge.
- **`Signal.Ref` panics on a signal with no wire name** instead of returning a
  bare `"$"`. A `Signal` reached through a pointer, slice, array or map field
  has no field name — rendering it already panicked; `Ref` used to hand back an
  expression that parsed and did nothing.
- **A nil child now renders as nothing** rather than panicking, matching the
  documented zero `h.H` and `html/template`: `h.Div(nil)` and the helper that
  returns `nil` on its empty case are both fine.
- **The zero `Router` is usable**, like `http.ServeMux`: `new(Router)` no
  longer nil-dereferences at the first `Mount`. Prefer `NewRouter` when you
  have options to pass.
- **`via.Live` and `OnConnect` are gone: there is ONE hook, `Initer`/`OnInit`.**
  `OnConnect(*Ctx) error` and `OnInit(*Ctx) error` had the same signature, the
  same Ctx powers, and ran at the same point; keeping both meant a composition
  with nothing to load still had to write an empty method to flip a liveness
  boolean. Rename `OnConnect` to `OnInit` and delete it outright where its body
  was `return nil`. Liveness is no longer an interface assertion but what a
  unit DOES: it is live if its `OnInit` registered a `Tick` or a `Listen`, or
  its `View` rendered a `State`/`List`. A unit whose server state only drives a
  branch (`State.Get()` in an `if`, never `Display()`ed) is not detectable that
  way; give it a `Tick` or render it.
- **`OnInit` now runs on every request that renders the unit**: the GET, each
  action, and the SSE connect, rather than once per connection. Register
  connection-scoped side effects, never perform them: the new
  `ctx.OnConnect(fn)` is the acquire half of `ctx.OnDispose(fn)` and runs only
  when a stream actually opens. `ctx.Listen` subscribes lazily for the same
  reason, so a page fetched but never connected no longer orphans a `Sub` per
  GET.
- **A failed `OnInit` runs no disposers**, because nothing was acquired yet:
  the acquire/release pair is `OnConnect`/`OnDispose` and neither half runs.
- **Every action now echoes the tab id**, live or not: `viatab` is declared
  on every page's `<body>` and every `@post`/`PostForm` carries it. A stateless
  page sends the empty id, which matches no connection and falls through to the
  stateless path, as does a plain child embedded on a live page.
- **The tab id is a SIGNAL, not the `X-Via-Tab` header** (wire break). Datastar
  builds request headers per call (`Object.assign({}, {Accept,
  'Datastar-Request'}, opts.headers)`, with no ancestor inheritance and no
  config hook), so a header had to be spelled out on every binding (33 bytes
  each). It filters out only signals matching `/(^|\.)_/`, so dropping the
  leading underscore is all it takes for the tab id to ride in the signal store
  every `@post` already sends. The header is no longer read. Security is
  unchanged: the id is still a synchronizer token set by same-origin JS and
  never auto-attached by the browser, the `Datastar-Request` check stays, and
  the origin floor is untouched. `PostForm` is the one exception: a native
  form submit carries neither signals nor headers, so it keeps its hidden
  `_viatab` field, now bound to `$viatab`.
- **A signal's wire name is its Go FIELD name** (wire break): `count`,
  `chat__draft` for one inside an embedded `Chat`, `outer__mid__kid__step` for
  a deeper path, replacing the opaque field offsets (`f0`, `f48`, `i0_f0`).
  The offsets stay as the internal key, so hydration is unchanged and
  reflection runs once per composition TYPE at `Mount`/`Embed`, never per
  render. A plain nested struct joins with one underscore, an embed boundary
  with two, so a parent that binds `p.C.S` itself (`c_s`) and also embeds
  `p.C` (`c__s`) keeps the two copies apart. Two fields of the same type in
  one parent are ambiguous — `Embed`'s argument order need not match
  declaration order — so those fall back to the positional key (`i0__s`).
- **`Signal[T].Ref()`** returns the signal's Datastar expression (`"$count"`)
  for hand-written attributes the typed API does not cover:
  `h.Data("show", p.Open.Ref())`. Field-held signals are named before the View
  runs, so `Ref` reads the same name wherever it is called.
- **`h.SafeURL` is gone.** The URL policy (http/https/relative admitted,
  `javascript:`/`data:`/protocol-relative refused, including a leading `\` or
  `/\` (WHATWG parsing treats `\` as `/`, so `/\evil.com` is protocol-relative
  too) moved to `internal/hcore`, where `h`'s typed attributes and via's
  `Redirect` gate share one implementation. It was exported only to cross a
  package boundary, and it carried a second copy of the three checks: two
  gates that agreed today is how one of them later admits a `javascript:`
  target the other refuses. Nothing outside via needed it; the typed
  `h.Href`/`h.Src`/`h.Action` attributes and `via.Redirect` enforce the
  policy for you.
- **`via/sess` merged into the root package**: `Session` is a real type with
  `Put`/`Get[T]`/`Clear[T]`/`Rotate` methods, reached via `ctx.Session()`; the
  `sess` subpackage and its `internal/sessbridge` shim are gone.
- **Bare mutators**: `Signal.Set(v)`, `State.Set(v)`, `List.Append(v)`, with no
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
  lie. The same sentinel works from an embedded child's `OnInit`.
- **Sessions are always on** (lazily — the cookie is only issued on the first
  write); the session options are tune-only. Key resolution:
  `WithSessionKey` → `VIA_SESSION_KEY` → random per-process key (warned at
  first mint).
- **Origin floor is open by default**; `WithTrustedOrigin` turns enforcement
  on (`WithInsecureOrigin` removed). The per-tab id remains the CSRF token on a
  live page; a plain page relies on the session cookie's `SameSite=Lax`.
- **`h` is elements + attributes + `Str` only**: the render plumbing
  (`Dyn`/`DynAttr`/`NewRenderer`/`Renderer`/`Binder`) moved behind
  `internal/hcore`.
- **One doorway each**: conditionals are `via.When` (no `If`), growing lists
  are `via.List[E]` (`State[[]E]` + `Append`), session rotation is
  `ctx.Session().Rotate()`. Themes are your CSS, not options; the SSE reconnect
  manager is always on.

### Added

- **Embedded children get `OnInit` too.** Only the root's ever ran; now every
  `via.Embed`ed child gets its own data-loading hook, before its own `View`,
  with the same answers — `via.ErrNotFound` → 404, `ctx.Redirect` → 303, any
  other error → 500 — even though it fails from inside the parent's render.
- **`ctx.OnConnect(fn)`**: run fn once when this unit's stream opens.
  The acquire half of `OnDispose`, and the only correct place for a
  connection-scoped side effect now that `OnInit` is per-request.
- **`topic.Topic.Subs() int`**: the live subscription count, for publishing
  presence and for proving a subscription was actually released.
- **`topic` delivery is lossless, and `ctx.Listen` renders once per batch.**
  The broker used to give each subscriber a 64-value buffer and drop anything
  past it, so a 1000-message burst to 100 subscribers lost ~87% of it — and
  since every delivered value drives one `Listen` handler call, an app that
  counted or accumulated in its handler silently got a wrong answer. Each
  subscriber now holds an unbounded-up-to-`topic.DefaultLimit` (100k) queue;
  `Publish` still never blocks, and a unit may publish to a topic it also
  listens to. `Listen` drains the whole backlog, runs every handler in publish
  order, then re-renders and pushes **once** for the batch, so a burst no
  longer costs one SSE frame per message. Loss is possible only past the queue
  limit, which means the reader is wedged rather than merely behind; it is
  counted by `Sub.Dropped()` (readable when you hold the `Sub` yourself —
  `ctx.Listen` owns its subscription, so there it shows up only in the log)
  and logged once per subscription. **Breaking:**
  `Sub.C() <-chan T` is replaced by `Sub.Ready() <-chan struct{}` plus
  `Sub.Drain() ([]T, bool)`; `Topic.SubscribeLimit(n)` sets a custom limit.
- **A plain (non-live) page may embed live islands** as sibling struct
  fields; each streams and patches independently over the page's one
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
  emits no attribute at all, which `RawAttr` could not express.
  `RawAttr` and `Data` stay for everything else.

- **`List[E]` gets `Remove`** alongside `Append`: it panics on an out-of-range
  index rather than silently doing nothing. Rows that can be removed or
  reordered need a stable `id` so the morph matches by identity rather than position.
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
  read off the request's `Datastar-Request` header rather than the route. `/_via/f/`
  is gone.
- **Path params**: `Mount("/thread/{id}", …)` + `ctx.Param[T]("id")`, on Go's
  own `http.ServeMux` syntax, named not positional; a segment that doesn't
  decode is an honest 404 on every stateless transport, and naming a segment
  the mount pattern doesn't have panics at request time (a wiring mistake).
- **`ctx.Listen[T](topic, handler)`**: subscribe + pump + auto-dispose
  in one line.
- **Arg events**: `via.OnArg` carries a typed render-time datum with the
  event: a per-row action without an `&` at the call site.
- **Native forms**: `via.PostForm` (server-side submit + 303). Always
  multipart, so a file `<input>` just works; read it with stdlib's
  `ctx.Request().FormFile(name)`; no separate upload verb or type.
- **`ctx.Redirect`** navigates from OnInit, OnReload, a PostForm submit (303)
  and a Datastar `@post` alike. Targets are gated by the shared URL policy;
  unsafe ones are dropped loudly with an element-patch fallback. A `@post`
  answers with a constant `location.assign` script Datastar executes through
  its `text/javascript` branch, with the target in a
  `datastar-script-attributes` header so the CSP can admit the bytes by hash.
- **`WithDocumentHead(via.Head{...})`**: the document shell. `Title`, `Lang`,
  `Raw` head markup (emitted verbatim after via's own `<meta charset>`), one
  inline style, and `ScriptOrigins`/`StyleOrigins`/`FontOrigins` are the app's
  own origin declaration for whatever `Raw` references, one list per
  directive so a stylesheet CDN isn't also script-trusted. `script-src`,
  `style-src` and `font-src` are derived from those lists, so a declared host
  works under the strict CSP and an undeclared one stays blocked.
  `InlineStyle` is admitted by its own sha256. Malformed heads panic at
  `Register`; the zero `Head` serves what via served without the option.
- **Resilience floor** — the fixed, non-configurable guarantees a live stream
  makes about surviving a flaky network: SSE keepalive comment frames (fixed 25s), per-frame
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

- **`ctx.Listen` no longer costs a goroutine per subscription per connection.**
  Each subscription used to run a reader goroutine (8KB of stack) purely to
  bridge its ready channel onto the connection's select loop — about a third of
  all goroutines at scale (15k at 5,000 tabs x 3 listens). The connection now
  owns one coalescing wake channel that every subscription signals
  (`topic.Sub.Notify`), and `runStream` sweeps its listeners itself. Measured:
  8 -> 5 goroutines per connection at three listens, and publish-to-handler
  latency 112us -> 70us. Handler order across two `Listen`s on one unit is now
  registration order rather than a race between reader goroutines. Delivery is
  unchanged: no handler call is lost, a backlog still coalesces to one render,
  a slow client still cannot head-of-line block another, and a unit may still
  publish to a topic it listens to.
- **Action ids are memoized.** `actionID` resolved a handler's Go name through
  `runtime.FuncForPC` and hashed it once per binding PER RENDER (~190ns each).
  The id is a pure function of the code pointer and the receiver's offset, so
  it is now computed once per pair. A thousand bindings render in 281us,
  down from 436us. The receiver-offset folding — which is what keeps two
  instances of one type from sharing an id — is part of the memo key.
- **`topic.Sub.Notify(ch)`** routes a subscription's wake-ups to a shared
  channel alongside `Ready`, so one reader can multiplex many subscriptions on
  a single select.

- **Vocabulary: one word per concept.** "live" was carrying three meanings and
  its opposite was spelled four ways (`stateless`, `plain`, `non-live`,
  `static`). Now: a page **streams** (the per-tab SSE connection) or is served
  **plain**; **live** is an adjective on a unit or composition only — a live
  unit pushes; **embed** replaces "island" as the noun for an embedded child.
  The rule the vocabulary buys: *a page streams iff it contains a live unit; a
  live embed may sit under any plain ancestor; a live unit may not contain
  another live unit.* Renames: `ctx.OnLive` → `ctx.OnConnect`,
  `vt.App.IslandAction` → `vt.App.EmbedAction`, `vt.Action.Live` →
  `vt.Action.Over`. Several 410/500 bodies and the nesting panics were reworded
  to match. **No wire change to ids or routing**: container ids, signal
  prefixes, action and SSE paths, and the tab/session headers and cookies are
  all untouched. Some error BODIES did change text (`no such island` → `no such
  embed`, `live connection closed` → `stream closed`), so a client matching on
  a 410 body string needs updating; the status codes did not move.

- **A native `PostForm` submit inside a live unit now returns the page a
  fresh connection will hold** (fresh instance, `OnInit` run) instead of a
  snapshot of the dying connection's mutated state, which the new stream
  reseeded anyway. Persist across a native submit through the session or a
  shared pointer dep, or `Redirect`.
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
  built, then pushes once: one render per action instead of two. An action id
  outside that table (a click racing a push, or a branched `View` that
  shifted it) now answers `410` instead of silently doing nothing.
- **Signals are addressed by field identity rather than render position.** A
  `Signal[T]`'s wire name is its Go field name, keyed internally by its byte
  offset within the composition struct — `count`, `chat__draft` for an
  embedded island — replacing the render-order `s0`/`s1`/`i0_s0`. This is a **wire break** with no code to port: a tab open
  across the upgrade posts the old names, the server ignores what it does not
  recognise, and the page is correct on reload. `via.Embed`'s signature is
  unchanged — the child copy it already takes by value is the offset base.

  It fixes conditional `Bind()`. Slots were claimed in first-render order, so
  a `Bind()` inside a `When` (a wizard step) could claim a slot another signal
  already owned: on a live page the post then wrote the WRONG FIELD, and on a
  stateless page the new input came up holding the previous occupant's value.
  A `Signal` reached through a pointer, slice, array or map field — or held by
  a composition whose `View` has a value receiver — is outside the struct, has
  no offset, and now **panics at `Mount`/`Embed`** rather than falling back to
  a render-order name that carried the old aliasing hazard. A Signal behind an
  INTERFACE field is invisible to the type walk and still panics on render. Make it a direct struct field. Keyed per-row
  signal slots remain future work.
- **A plain action hydrates signals inside a branch another posted signal
  opens.** Discovery renders once on server state alone (that render, and only
  that render, decides what is dispatchable), applies the body, and re-renders
  to a fixpoint so a `Bind()`ed `Signal` in a `When` build, an `Each` row, or
  an embed `View` that another `Bind()`ed signal reveals is hydrated too — its
  posted value used to be dropped server-side and wiped client-side. The
  dispatchable set is the INTERSECTION of the two renders, never a superset,
  and the intersection is per `(handler, arg)` pair: a posted signal cannot
  open the branch that authorizes a handler, nor widen an `Each` so that an
  `OnArg` arg the server-state render never bound becomes dispatchable. A
  handler that exists ONLY inside such a branch still answers 410 on a plain
  page. Every discovery pass re-renders the whole tree, so an embedded child's
  `OnInit` runs once per pass (two passes for a page whose posted body carries
  any `Bind()`ed slot) — keep `OnInit` cheap and idempotent.

  A stateless action's patch now also declares any slot the pre-action render
  did not carry, so an input that appears for the first time in the response is
  seeded instead of inheriting whatever the client store still held.
- **Actions are addressed by handler rather than by render position**
  (`/_via/a/{island}/{id}`, plus `?a=` for a value-carrying action). `id` is a
  short hash of the handler method's fully-qualified Go name, so it is stable
  across renders, across instances and across rebuilds — a deploy does not
  invalidate the URLs open tabs are holding. This is a **wire break**: a tab
  open across the upgrade 410s its first click, then reloads correct.

  It replaces the `?v=` shape digest, which is deleted. The digest fixed the
  signal order and the action COUNT, so on a page backed by a shared store
  every per-row `OnArg` was its own action slot and another user adding or
  removing a row silently 410'd every other open tab's buttons — untouched
  ones included, permanently, until reload (`example/poll` demonstrated it).
  The digest also did no security work: CSRF is the origin floor plus the
  per-connection tab id, and the digest never stopped a forged in-range `n`.

  A click now 410s only when the current render does not bind that handler at
  all — a closed branch, or the classic wiring mistake of an `OnInit` that
  fails to restore the state the `View` branches on. The SERVER LOG then names
  the handlers the render DID bind, so the mistake reads as a diagnosis instead
  of a dead button; the 410 body names only the id that was asked for, since
  the bound list is the render's Go type and method names. Two bindings of the same handler (same method, same `?a=`)
  collapse onto one entry, which is what they mean.

### Fixed

- **A `Set` inside a `Tick`/`Listen` handler now reaches the client.** A live
  push omits `data-signals` (so a morph never clobbers what the user is
  typing), and the tick path emitted no signal patch of its own — so a server
  write from a timer or a `topic` listener updated server memory and nothing
  else, and on a `Bind()`ed slot the push's display render painted the client's
  own last-posted value straight back over it. Every push now ships the unit's
  dirty set as its own `patch-signals` frame and drops those slots from the
  client table first, which is what a live action already did; the action path
  now goes through that same code rather than its own copy.

- **A panic in a push's display render no longer strands the client's posted
  values on the live unit.** The stream survives such a panic (it is logged and
  the frame dropped), but the restore that undoes the display render's
  hydration was a trailing call, so the unwind skipped it and the next
  `Tick`/`Listen` handler read client-controlled data off the instance. It is a
  `defer` now.

- **The live (SSE) path now derives its action table from a render the client
  did not influence.** The two-phase discovery above was plain-only. On a live
  page the unit outlives the request, so a hydrated `Bind()`ed signal became the
  server's own state: it opened a `via.When` branch, the branch's handlers
  entered the connection's dispatch table, and they stayed callable for the life
  of the stream — reachable both from the SSE connect body and from the body of
  any action the client was already allowed to call. Every push now renders the
  AUTHORITY first (server-authored values only), applies the client's signals to
  it for a DISPLAY render, and registers the intersection of the two, per
  handler and per arg. The SSE connect body no longer hydrates the render that
  decides liveness and actions at all; it is kept and applied to display renders
  instead. **Breaking:** a `Bind()`ed signal's posted value no longer persists as
  server state between requests on a live page — it is re-applied to each
  display render wherever that render still binds the slot, which is exactly
  what the plain path has always done. A push undoes the display render's
  application before it returns, so reading one in a `Tick`/`Listen` handler, or
  in a `View` that no longer `Bind()`s it, sees the server's value.
  **Cost:** a page containing any `Bind()` pays TWO renders per push, not one —
  a real Datastar connect body is the whole signal store, so the client's posted
  slots are never empty once the page has a bindable signal. Each extra render
  also re-runs every plain child's `OnInit` (`inheritRequestScope` sets
  `doInit`) and re-walks the live-nesting check, so keep a plain child's
  `OnInit` cheap or hold the data on the live root.
  A page's live units share ONE revert set for the life of the connection: a
  per-push set left a second live unit's registered `Ctx` pointing at a set
  nothing restored, which re-opened the escalation above on any page with two
  live units.

- **A `ctx.Param` that no longer decodes answers 404 on the live path too.** It
  was the plain path's 404 and the live path's 500-plus-stack-dump for the same
  URL and the same sentinel.

- **One unmarshalable signal value no longer wipes `data-signals` for the whole
  page.** The declaration was marshalled as a single object, so one value
  `encoding/json` refused emptied the attribute for every signal on the page,
  silently, with the page still rendering. The offending slot is now logged and
  dropped on its own.

- **A client-posted signal can no longer open a server-gated branch.** A
  `Signal` is hydrated from a request only when THIS render put it under client
  control — i.e. only `Bind()` (which emits `data-bind`) makes a slot writable.
  A `Display()`-only signal, or one the `View` never rendered, is state the
  server publishes downward, and an inbound value for it is now ignored on
  every path. Separately, the plain action path no longer hydrates during its
  discovery render at all: the body is applied AFTER that render, the way the
  live path has always done it. Together these restore
  dispatchable-iff-rendered — before, a `Signal` an `OnInit` filled from the
  session could be flipped by the POST body, opening a `via.When` branch and
  minting the very `(handler, arg)` pair the dispatch was then checked against.
  **If you were gating a branch on a `Display()`-only or unrendered signal and
  relying on the client's value, that no longer round-trips: `Bind()` it (and
  accept that it is then client-controlled), or move the gate to session or
  database state.**
- **A native `PostForm` on a plain root no longer kills a live embed on the
  page.** The full page such a submit answers with is what the browser replaces
  the document with; it was written with the stream bootstrap hard-coded off,
  so a plain root carrying a live `Embed` served a document with no `data-init`
  and the embed was dead after the first submit. Liveness is now read off that
  re-render, exactly as the GET and the live path do.
- **The acted-instance substitution now checks the TYPE as well as the key.**
  When a root's `Embed` order shifts between the discovery render and the
  response re-render (the acted embed's own action opened a branch or appended
  to the list the root iterates), the acted key names a slot a different type
  now occupies. The mutated instance was spliced in there regardless: the
  wrong `View` rendered under the wrong slot prefix, and the type that belonged
  there vanished. On a type mismatch via now falls back to the fresh copy and
  its `OnInit`.
- **A failing child `OnInit` on the push path no longer kills the stream
  silently.** A plain child of a live root is re-inited on every frame, so one
  whose `OnInit` returns an error/`Redirect`/missing param panicked every
  frame; each was logged and dropped and the tab stopped updating, with
  no client-visible signal. The stream is now torn down once (logged once), so
  the client reconnects and the GET answers the failure as the 500/303/404 it
  actually is.
- **A value-carrying action (`OnArg`) now authorizes its `?a=` against the
  render.** The discovery render rebuilds the set of args it binds for that
  handler, and a POST whose `?a=` is not in it answers 410 before the handler
  runs — so another user's row id, or one behind a `via.When` branch closed for
  the caller, is not dispatchable. **This is a behaviour change for any app that
  binds a VOLATILE value as an arg** (a pagination cursor, a count): such an arg
  goes stale the moment a render moves it and the click 410s. Bind a stable
  identity (a primary key) and read changing state off the composition instead.
- **A plain embed's action response keeps the instance the handler mutated.**
  The re-render walks from the root and `via.Embed` re-copies the parent's
  field, so a form's validation error and the values the user typed were
  discarded and the response came back pristine, as if the POST had never
  happened.
- **Wire break: the positional embed slot prefix is `i0_0__`, not `i0-0__`.**
  Only the fallback prefix minted when a parent holds two fields of the child's
  type is affected. The key's depth separator `-` is not a JS identifier
  character and `Ref()` hands these names straight to Datastar expressions, so
  it is spelled `_` in the slot name. Container ids and dispatch URLs still use
  `-`.

- **A stateless action's response render now runs its nested children's
  `OnInit`.** The acted-on unit's own `OnInit` already ran for the request, but
  every embedded child in the patch is a fresh copy whose `OnInit` never had —
  so a nested child that loads its data there came back zero-valued in the
  patch, nothing like what the same subtree renders on a GET. Both the root
  patch and a plain embed's own subtree patch are fixed. A live push does the
  same, and the cost is real: **a live root re-runs every plain embedded
  child's `OnInit` once per pushed frame**, so any side effect in such an
  `OnInit` repeats at the frame rate. Keep a plain child's `OnInit` cheap and
  free of side effects, or hold the data on the live root and pass it down the
  field. (A `Tick`/`Listen` registered there is snapshotted at connect and does
  not accumulate.)

- **Wire break: an island is addressed by its KEY, not a page-wide counter.**
  An `Embed`ed child's identity is now its ordinal among its own parent's
  `Embed` calls, composed onto the parent's key — the root's children are
  `0`, `1`, …, a child of `0` is `0-0` — and the root's dispatch address is
  `r`. Container id (`via-i0-0`), signal prefix (`i0_0__`) and dispatch
  address (`/_via/a/0-0/…`) all read that one key. The flat counter it
  replaces was allocated by a whole-page walk, so a PLAIN island's own action
  re-render — which renders only its own subtree — restarted numbering at the
  root: a nested live child came back carrying a DUPLICATE of its parent's
  container id, a signal prefix that aliased the parent's own slot, and a
  dispatch address that answered `410 no such action` on click. A tab open
  across the upgrade holds the old addresses and gets one dead click before a
  reload, exactly like the action-URL break above.
- **A live island under a parametrised mount is no longer dead in a browser.**
  The GET handler advertised the stream as the route PATTERN
  (`data-init="@post('/job/{id}/_via/sse')"`) while every other URL on the
  page carried the concrete request path. The browser POSTed the pattern
  literally, missed the route, and the page's live regions never connected.
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
- **`OnInit` now runs on an embedded island's action**, where before only the page GET
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
- A value-carrying action (`OnArg`) with a malformed, empty, or `null`
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
  "establish the session in `OnInit`" pattern — now binds too, on that
  first mint; previously only a session that already existed at connect time
  was covered, so a tab that logged in mid-connection kept accepting a
  cookieless dispatch until reload.
- **A panicking `OnDispose` function no longer skips every disposer
  registered after it.** Each disposer now runs through the same per-item
  recover a Tick/Listen/action pulse already gets; a skipped disposer (e.g.
  `sub.Stop`) used to leak its subscription for the life of the process.
- `Tick`/`Listen` called after `OnInit` has returned now log loudly
  instead of silently registering nothing.
- A panic before a live stream's headers are sent now answers 500 instead of
  falling through to Go's default 200-with-empty-body.
- `WithSessionKey`/`VIA_SESSION_KEY` under 16 bytes now panics at
  construction instead of silently signing cookies with a guessable key.
- **A plain root's own action no longer repaints its embedded live islands
  from their seed:** live containers carry `data-ignore-morph` and a live
  island's push patches its children (Datastar inner mode), so a root patch
  leaves the live DOM alone.

### Known limitations

- A live connection's tab id is a bearer credential for that connection's
  actions until it binds to a session (at connect, or on first login
  afterward); an anonymous connection has no session to check against at
  all, so never render, log, or leak a tab id outside its own client.
- An island's key is its ordinal among its parent's `Embed` calls, so a `When`
  wrapped around an `Embed` renumbers every LATER sibling of that parent when
  it flips. Composition made the numbering stable across partial re-renders;
  it did not make it stable across a shape change, so such a `When` must still
  depend only on data fixed by `OnInit` or the field literal.
- There is no per-IP or per-tab cap on concurrent SSE connections beyond the
  router-wide `WithMaxLiveConnections`; a single client can still open many.
- Action-body JSON decoding is not strict: unknown signal keys and trailing
  bytes after the JSON value are ignored rather than rejected.
- Session idle-TTL eviction is lazy, enforced on the next access rather than swept
  proactively — so a session nobody ever touches again outlives its TTL in
  memory.
- A `Tick` handler that blocks (I/O, an unbounded loop) pins the connection's
  one goroutine, which defeats a clean shutdown of that connection until the
  handler returns.
- A session that idles past its TTL while a live stream is open turns every
  later dispatch on that tab into a 403 "session mismatch" until the page is
  reloaded — the stream itself does not keep the session warm.
- A `View` that panics PART-WAY through a live push's display render can lose
  exactly one later `Signal.Set`. `Signal.bind` stamps the signal's dirty sink
  at bind time, so the signals rendered before the panic point are left
  pointing at a `Ctx` the push never installed; a `Tick`/`Listen` `Set` before
  the next push lands in that orphaned map, the push's `flushDirty` does not
  see it, and the display render repaints the client's value over it. It
  self-heals on the following push, and it is not client-driven — the `View`
  has to panic. Keeping the dirty sink on the CONNECTION instead was
  considered and rejected: the plain path has no connection, and it reads the
  acted embed's OWN dirty set (not the page-wide one) to scope that embed's
  `data-signals` patch, so one shared sink would make an embed's patch
  re-declare a sibling's slots and clobber a value the user is mid-edit.

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
  that point. This is deliberate, but it is a behaviour change.
- A session minted from a `Tick`/`Listen` handler (as opposed to an action
  or `OnInit`) has no open response to carry a cookie, so it is created
  and then orphaned until its TTL. In some configurations this is silent:
  if the connection's `OnInit` ever calls `Session()` at all — including
  a read-only `Get`, the pattern this doc recommends — the resulting
  handle is cached with the connect response already attached, so a later
  `Tick`/`Listen` `Put` writes its `Set-Cookie` onto that dead response
  with no warning logged. Establish sessions in `OnInit` or an action
  instead.
- A `Tick`/`Listen` handle keeps the session id it connected with, so once
  another request rotates that id away (a login in a plain action), every
  session write from that handle is dropped with a log for the rest of the
  stream, and `Rotate` on it is refused rather than re-issuing a cookie over
  the one the browser now holds. Reads still serve the connect-time snapshot.
  Reconnecting the stream picks up the new id.

Earlier releases (v0.7.0 and back) predate this changelog; see the git tags.
