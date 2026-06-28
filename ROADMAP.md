## Build log + refinements (post-council)

What is built on this branch, and where implementation refined the plan:

- **Slice 1 (hardened stateless core): DONE.** All eight do-now items landed on
  `/v2`, `-race` green, gofmt/vet clean, adversarially reviewed (0 material
  findings), HTTP-verified live.
- **Reactive handles (part of slice 3): DONE for `Signal[T]`.** `.Node()` →
  `.Display()`; two-way `Bind()` fixed (it was emitting an empty `data-bind`);
  the `greeting` example proves live client-side two-way binding. Remaining
  slice-3 work (signal-patch response leg / round-trip via `State`, `Op(ctx)`
  mutators, `Local[T]`) is still pending.
- **Live islands (slice 4) minimal vertical: DONE and BROWSER-VERIFIED.** The
  `Live` interface (`OnConnect`), `Ctx.Tick`, a single per-tab `GET /_via/sse`
  stream, the head `data-init` bootstrap, and `event: datastar-patch-elements`
  push (multi-line-safe framing) ship; the `pulse` example pushes a server beat
  that Datastar morphs into the page. **K4 satisfied**: `vtbrowser/` is a
  separate module (chromedp kept out of core's deps) with a `-tags browser` test
  that drives headless Chromium and asserts the DOM morphs on server push
  (beats 0→7). **Reconnect floor + resilience: DONE on `feat/v2-bare-core`** — a
  keepalive comment frame (`WithSSEHeartbeat`), a per-frame write deadline
  (`WithSSEWriteTimeout`), write-error/half-open teardown (a failed frame write
  cancels the stream so the island goroutine + timers don't leak), and main's
  reconnect IIFE ported verbatim (opt-out `WithoutSSEReconnect`). The SSE GET is
  now origin-checked (the same `originAllowed` floor as the action POST) and
  capped at a configurable number of concurrent streams (`WithMaxSSEConnections`,
  default 10,000; over-cap returns 503). Still pending in slice 4+: the
  multi-island per-tab SSE multiplex (today one island = one stream) and
  per-connection authentication of the stream (the `via_tab` handshake lands with
  `via/sess`, slice 9).

  Follow-up (tracked): the cap's 503 is returned on the same `/_via/sse` the
  reconnect manager retries. Against a persistently-full server, a rejected
  client backs off → `retries-failed` → reloads → re-fetches `/` (uncapped, so it
  always serves) → re-arms SSE → 503, a reload-storm rather than a clean "server
  busy" UX. Mitigation deferred: a 503-specific longer backoff in the reconnect
  manager, or a degraded static page served when over the cap.
- **Slice 5 (`State[T]`): DONE and BROWSER-VERIFIED.** Server-authoritative,
  per-connection island state: `Get`/`Set` plus a `Display()` that renders the
  literal escaped value and is element-patched (morphed) on change. The K2
  pre-connect contract is enforced as designed — a render-time `island` flag is
  threaded through every render path, so `State.Display()` reads fine on a live
  island's first paint (before OnConnect) and **panics** only on a non-island
  render (a `State` dropped into a stateless page). The `pulse` example now uses
  `State[int]`, so the live + browser tests exercise State end-to-end (zero value
  at first paint, increments over SSE). Pending: kill the remaining shim so a
  bare handle works, and `Local[T]`.
- **Slice 6 (Topic / Subscribe / OnDispose): DONE and BROWSER-VERIFIED.** A
  blessed `via/topic` sub-package (`Topic[T]`: `New`/`Publish`/`Subscribe`/
  `Sub.C`/`Stop`, non-blocking drop-on-full so one wedged tab can't stall the
  broadcast) keeps the core free of shared state. The seam is a free
  `via.Subscribe[T](ctx, ch, handler)` (Go has no generic methods, so DESIGN's
  `ctx.Subscribe` became a free func — still zero `&`, handler is a method value)
  plus `ctx.OnDispose`. Ticks and subscriptions share ONE island goroutine
  (serialized, no lock); disposers run on disconnect. Proven: one publish fans
  out to every connection, OnDispose tears down, no goroutine leak / no
  send-on-closed (audited under `-race -count=5`, Topic 100% covered), and the
  `feed` example morphs in headless Chromium on an external publish.
  **KNOWN BOUNDARY:** interactive "user posts a message" needs a live-island POST
  action to mutate *this connection's* island instance — but the action handler
  runs on a fresh per-request copy, so per-connection action routing (the
  `via_tab` handshake, slice 9) is required first. Broadcasts from an external
  producer (a ticker, a bus, a DB consumer) work today.
- **Live-island action routing (the `_viatab` handshake): DONE and
  BROWSER-VERIFIED — the keystone.** A per-Register `registry{tabID→liveConn}`;
  the SSE connect mints a 128-bit tab id, registers the connection's pulse
  channel, and pushes a `_viatab` patch-signals frame; `OnClick` on a live render
  emits `@post(...,{headers:{'X-Via-Tab':$_viatab}})`; the POST live branch looks
  up the conn (miss→410), skips `shapeMatches`, and `Dispatch`es the action onto
  the island goroutine (re-binding on the LIVE instance), acking 204 while the
  SSE push ships the patch. `runLiveStream` now receives the pulse and always
  loops; teardown deletes the registry entry. Blue-audited: concurrency-correct,
  race-clean under `-count=5`, no leak, origin-floor + unguessable-id CSRF. The
  chromedp gate passes — a real-browser click round-trips `count 0→1` through the
  header. This is what makes via genuinely interactive + multi-user.
- **Slice 8 (compose: `Each`/`If`/`When` + list tags): DONE.** `compose.go` —
  `Each[T](items, row)` renders each row in place (append-only morph; add a row
  `id` for keyed reorder/delete), `If(cond, node)` eager, `When(cond, build)`
  lazy. `h.Ul`/`Ol`/`Li`/`B` added. Lint extended (`When`/`Each` reject a closure
  arg; a method-value row/build passes). DEFERRED (not needed by the chat, which
  has display-only rows + one trailing action so the flat action counter stays
  stable): `Embed` (multi-island composition) and the K1 structural-path cursor
  (only needed for *action-bearing* dynamic shape — a list whose rows carry their
  own actions). Documented in compose.go.
- **Slice 9 (ergonomics sugar): DONE.** `List[E]` (embeds `State[[]E]`, adds
  `Append`) for a growing log; `Local[T]` (client-only underscore signal, no
  server `Get`/`Set` doorway, `Bind`/`Display`); `OnSubmit`/`OnInput`/`OnChange`
  (refactored `OnClick` into a shared `onEvent` — Datastar AUTO-prevents a form's
  default submit, verified in the bundle, so no modifier); `Counter`+`Op(ctx)`
  arithmetic verbs (replaces `Num`, now removed). Correction to the council's
  ideal chat code: `Who` must reach the server to author a message, so it is a
  `Signal` (round-trips), not a `Local`; the chat uses Signals for both inputs.
- **Slice 10 (chat showcase): DONE and TWO-BROWSER-VERIFIED — feature-complete.**
  `via.WithTheme()` (classless nonce'd stylesheet + `style-src` CSP); `example/chat`
  (Room with messages+presence topics; Chat island Signal Who+Draft / List Log /
  State Online; OnConnect subscribe+presence+OnDispose; Send publishes + clears;
  `via.Each` log; `OnSubmit`) reads like a static page and passes the
  no-`&`/no-closure lint. The **signal-patch-response-leg is closed**: the live
  element push omits `data-signals` (so a fan-out morph never clobbers a client
  signal a user is editing) and deliberate server-driven signal changes ride a
  `datastar-patch-signals` frame (`Signal.Set` records a dirty map; the live
  dispatch flushes it). Two real headless browsers verify: a message in tab A
  appears in tab B, the presence count tracks connections, the composer clears on
  send, AND a concurrent draft is NOT clobbered by a fan-out.
- **Final whole-v2 review (6-agent panel): 4 blockers found + fixed.** (1) a bare
  `\r` in rendered content split the SSE element-patch frame — `h.writeEscaped`
  now escapes `\r`→`&#13;`; (2) `Signal.Set` on an unrendered signal silently
  drops the patch — documented as the render-visibility contract; (3) an
  `OnConnect` error skipped disposers (orphaning Topic subs) — disposers now run
  on the error path; (4) the README Status contradicted the as-built code —
  rewritten. The reflect-import lint now also scans `topic/`. Medium/nits left as
  honest follow-ups: no connection cap on the SSE (single-pod/internal scope —
  front with a proxy or add `WithMaxConnections`); `State.Get()` off-island isn't
  guarded (only `Display()` is); a no-op live action still pushes a full
  element-patch (no 204 symmetry on the live path); `Counter`/`Local`/`If`/`When`
  lack dedicated examples (tested, not demoed).
- **via/v2 is feature-complete** for the flagship showcase: stateless → reactive
  → live → server-`State` → multi-user-with-interactive-actions, on a small
  reflection-free core, no hand-written JS, browser-verified end to end.
- **Refinement to slice 2 — signals key by HANDLE IDENTITY, not structural
  position.** A `Signal[T]` lazily caches one stable wire name and reuses it
  across every render, so `Bind()`-on-an-input and `Display()`-elsewhere share
  one signal and update together. Structural-position keying (the council's
  slice-2 scheme) is correct for **actions** (each `OnClick` is a distinct
  action at a distinct spot) but wrong for signals (a signal has one identity).
  So slice 2 is re-scoped: **structural keys apply to actions only**, deferred
  to when `If`/`Each` (slice 7) create dynamic shape that breaks the flat action
  counter. Until then the flat counter + `shapeMatches` backstop are sound for
  stable Views.

---

## Headline verdict

Base the streamlined via on `experiment/bare-core` and ship it as a
parallel `github.com/go-via/via/v2` module. The council's principles,
do-now list, slice ladder, example set, and release strategy are
**adopted**. But four items the decision record marks resolved are, on
inspection of the actual code, **designed-but-unspecified or
self-contradictory**, and they sit on the critical path. This plan
ratifies the council's direction and **closes those four holes** with
concrete mechanism, plus three smaller corrections. Do not start slice 2
until the structural-key descent algorithm is ratified; do not start the
island slices until the pre-connect State contract and same-type Embed id
are ratified; do not call the browser tier a merge gate until a slice
actually builds it.

---

## Principles (kept)

1. Feels like normal Go net/http; via adds composition + Datastar sugar.
2. Server-rendered hypermedia; the browser renders, the server owns truth.
3. Stateless by default, live by opt-in (`OnConnect` ⟺ live island).
4. Composition is the single source of truth; wiring is derived.
5. Hard guarantees: no reflection, no user-facing identifier strings, no
   capturing/closure actions (named method values only), no `any` in
   element/child signatures, zero `&` at any user call site.
6. `View()` is pure and ctx-free.
7. Core stays ~1,400 LOC; table-stakes return as no-reflection
   sub-packages, not core bloat.
8. **Browser-observable behavior is gated by a chromedp tier that is a
   real, scheduled work item — not an aspiration** (see Residual Risk +
   slice 4).

---

## Resolved design decisions

Format: decision → resolution → why.

### Adopted as written (council verdicts stand)

- **Release strategy** → parallel `/v2` module, same repo, freeze main at
  v0.7.0 as read-only migration reference; cut over per-app → SIV makes
  v2 at the unversioned path a silent `go get -u` miscompile; `/v2` is the
  only correct mechanism.
- **post-response-shape** → element-patch is the slice-1 wire contract;
  per-handle-type dispatch deferred to the Signal/State slices → the code
  and green tests already vote element-patch; the JSON-patch leg re-renders
  signals at zero and is unsafe as wired.
- **csrf-auth-perimeter** → same-origin floor + body cap + recover + CSP
  baked into `Register` now; via_tab token returns when islands land →
  `shapeMatches([],{})` is true, so any cross-origin empty POST mutates
  state today.
- **handle-type-set** → `Op(ctx)` accessor over a shared `NumOps` bridge,
  not a `Num` embed → one autocomplete doorway, verbs written once.
- **bare-handle-vs-node-shim** → `.Display()` is the permanent spelling;
  drop the bare `h.H1(c.Count)` promise → the seal + per-request
  `inst := root` copy + positional binding make the bare form unreachable
  without reflection/`&`.
- **register-viewer-constraint** → `Register[T any, PT interface{*T;viewer}]`
  → turns a forgotten View from a first-request 500 into a compile error;
  net code deletion. Confirmed: GET and POST handlers BOTH carry the
  `any(&inst).(viewer)` + 500 branch (via.go) — both delete.
- **per-island-sse-vs-multiplex** → one multiplexed SSE per tab from day
  one; keep per-island lifecycle → the HTTP/1.1 ~6-connection cap is a
  correctness ceiling, not a tunable.
- **out-of-core-cut-list** → confirm the big cuts; return `via/sess`,
  `via/router`, and `RenderState[T]` → sessions/routing are table-stakes
  encoding a security model you must not fork per app.
- **reconnect-strategy** (✓ DONE on `feat/v2-bare-core`) → ported reconnect.go
  IIFE verbatim as the floor (reload-to-re-bootstrap on `retries-failed`);
  real-browser re-arm behavior still to be gated on a chromedp test → Datastar
  does not re-arm a clean close, so a graceful deploy freezes every tab without
  the floor.

### Corrected / made concrete (this Chair's revision)

- **D1 slot/action identity — structural-path key** → ADOPT the structural
  key, but with an **explicit renderer-descent algorithm** (below), because
  "fold child-offset + tag as the renderer descends" is **not computable
  through the opaque `Dyn`/If/Each closures the renderer cannot see into**
  (confirmed: `element.render` walks `kids []H` and a `dynNode` is an
  opaque `fn func(*Renderer)`). The key is position-stable only across
  renders of the **same shape**, which is exactly what If/Each break. →
  Without a stated descent algorithm, "misrouting is unrepresentable" is
  false for the `If(admin,Delete)→Save` case the decision cites as
  motivation.

- **D5 / do-now #2 — split the cut** → DELETE only `dirty`/`markDirty`
  (written in `Signal.Set`, never read on the POST leg). **KEEP**
  `initial`/`setInitial`: `writeSignalsAttr(&b, ctx.order, ctx.initial)`
  reads `initial` to emit the page-level `data-signals='{...}'`, and
  `TestDataSignalsDeclaresNumericSignalForHydration` asserts it. → As
  written, do-now #2 deletes the live hydration seed and regresses a
  passing test + the slice-3 round-trip.

- **D3 — Topic scope** → MOVE `Topic[T]` to a sibling sub-package
  `via/topic` (alongside `via/sess`, `via/router`), NOT core. → DESIGN.md's
  "via owns no shared state… apps bring their own and pipe it into an
  island via Subscribe" is a sacred invariant the plan leans on elsewhere
  (e.g. the ctx-free-View rationale). A framework-owned subscriber set in
  core contradicts it. A blessed sub-package keeps core's invariant
  CI-testable (grep core for shared-state primitives) while still shipping
  the one-line fan-out the chat headline needs. `ctx.Subscribe`/`OnDispose`
  stay in core (they are the island seam); only the broker is the
  sub-package.

- **D9 — ops port is an ADAPTATION, not verbatim** → port the `NumOps`
  verb set and `ops[T]` bridge shape, but re-found them on bare-core's new
  `Signal[T].Update(ctx, fn)` round-trip primitive, not main's
  server-authoritative `StateTab.Update` → main's `ops.update` closes over
  a stateful CAS/dirty path; bare-core Signal is client-resident and
  round-trips through re-render. Mark slice 3 "adapt," not "copy."

- **Bind() empty-slot bug** → FIX in slice 3, flag now → `Bind()` reads
  `s.slot`, assigned lazily inside `Node()/Display()`'s closure; attrs
  render before body children (`element.render` does all `Attr` kids
  first), so `<input data-bind="">` ships stale/empty. Resolution: under
  the structural-key scheme (D1) the slot becomes the input element's own
  structural key, claimed by the element, so `Bind()` reads a
  position-stable key that does not depend on a sibling `Display()`
  rendering first. Until D1 lands, `Bind()` must eagerly claim the slot.
  The `form` example (proof #2) is the first to exercise this and gates it.

---

## The four keystone mechanisms (ratify before the gated slices)

### K1 — Structural-path descent through dynamic shape (gates slice 2)

Problem: a structural key folded from "child-offset as the renderer
descends" is only position-stable when the tree shape is identical between
the GET hydrate-render and the POST bind-render. `If`/`Each`/`When` produce
data-driven shape, so the child-offset of an action *after* a conditional
is render-positional again — the exact thing the scheme replaces.

Mechanism (the descent contract slice 2 must implement):

- The key is **not** a running counter over emitted children. It is a path
  of `(structuralTag, ordinalAmongSameStructuralTag)` segments accumulated
  as the renderer descends the **static** element tree, hashed FNV-1a into
  a uint64, emitted base36.
- **`If(cond, node)` reserves its structural path unconditionally.** When
  `cond` is false the renderer still descends into the would-be subtree's
  structural skeleton (folding the same path segments) and emits nothing.
  This requires `If` to take an **eager `h.H`** (or a builder whose static
  shape is known): the present-branch keys are byte-identical whether the
  condition flips. `When(cond, build func() h.H)` is the lazy variant and
  reserves a single opaque slot path — anything inside it is keyed
  **relative to that reserved slot**, so siblings after a `When` never
  shift.
- **`Each` is the one place pure-path identity collides** (N rows share one
  static skeleton path). Mandate `Each[T Keyed]` / `Each[T any](items, row,
  key func(T) K)` where `K comparable` is folded into the per-row path hash
  (hashed, never emitted). Unkeyed/index lists are rejected (or
  compile-disallowed) for any island carrying actions. This contract ships
  WITH slice 2, not later.
- Net: a slot/action key is a function of **where the node sits in the
  static structural tree + its keyed list identity**, never of how many
  prior children happened to render. `shapeMatches`/`order`/the len-mismatch
  410 are deleted; a dispatched key resolves to the live action or is
  absent (410s exactly the gone action).

If K1 cannot be made to descend false `If` branches and reserve `When`
slots without re-introducing render-order counting, slice 2 stops and the
council reconvenes — D1 is the keystone for slices 3, 7, 8, 10.

### K2 — Pre-connect State[T] read contract (gates slices 4–5)

Problem: DESIGN.md first-paint says the page server-renders the island's
initial `View()` (constructor/zero-value state) so there is no empty flash
— but that GET runs `View()` BEFORE `OnConnect`/SSE, and the ctx-free
`View()` reads `State[T]`. Decision #2 mandates a hard panic on a State
read with no live island. These contradict at the exact boundary the clock
example proves.

Resolution: first paint establishes a **transient, read-only island
instance** for the render. `State[T].Get()` reads `s.val` off that
addressable instance (constructor/zero value at first paint; live value
after connect) — it is identity-by-instance-pointer, referentially pure,
and never zero-defaults. The render-time **panic fires only when a State
read occurs outside ANY island render context at all** (e.g. a `State[T]`
dropped into a stateless page's View), not during the island's own
first-paint render. Concretely: the panic guard keys on "is the current
renderer bound to an island instance (transient first-paint OR live)," not
on "has OnConnect run." Slice 4 (first paint) and slice 5 (State + panic
guard) jointly own this contract.

### K3 — Same-type Embed discriminator (gates slice 8)

Problem: `Dashboard{Hits, Visits Counter}` and `via.Embed(Counter{})`
twice both produce two structurally-identical children. With no field-name
reflection (banned) and no user strings (banned), the only remaining signal
is positional sibling order — which collides with D1's "identity is
structural not positional."

Resolution: an island's structural key is `parentPath ⊕
siblingOrdinalAmongSameStructuralChildType`. The ordinal among
**same-typed structural children** is part of the structural identity, not
a violation of it: it is stable as long as the composition's field/Embed
order is stable, which is a source-level fact (the composition is the
single source of truth), not a render-order fact. Adding a *different*-type
sibling does not shift it; reordering two same-type siblings in source is a
deliberate API-breaking change (same as renaming a field). `Embed`
re-anchors the path cursor to the child's own subtree base so cross-island
keys never collide. This is ratified **jointly with K1** (D1's `affects`
already says the two must co-ratify) before slice 8.

### K4 — Browser tier as a buildable, estimated work item (un-gates the gate)

Problem: the plan mandates "an island PR without a chromedp test is
un-mergeable" (Principle 6/8, Residual Risk 2, slice 4) but no slice ports
or builds the harness, and `vtbrowser/` in main is its **own go.mod
module** (`github.com/go-via/via/vtbrowser`, chromedp + cdproto deps, local
`replace` on the parent). Standing it up against `/v2` is real,
unestimated work. As written the plan bakes in the exact "browser tier
silently never runs" failure it names as risk #2.

Resolution — make it a first-class deliverable inside slice 4 with explicit
sub-tasks and effort:

- Create `via/v2/vtbrowser` as its own module (`module
  github.com/go-via/via/v2/vtbrowser`, `go 1.24`), `require`
  chromedp/cdproto pinned to main's versions, and a `replace
  github.com/go-via/via/v2 => ../` for local dev. **Effort: M** (module
  scaffold + re-root imports + re-point the local replace).
- Port `vtbrowser.Open` + the `VIA_BROWSER_REQUIRED=1` hard-fail gate and
  port `vt.SSE`/`AwaitFrame`/`SSEReady` (these live in main's root module
  today) into v2 test helpers. **Effort: M.**
- The CI policy "island PR without a chromedp test is un-mergeable" is only
  declared once these two tasks land and the smoke suite (EventSource opens
  once; page-morph-above does not double-connect; click round-trip;
  apostrophe inertness) is green. Until then the gate is documented as
  "pending K4," NOT enforced — no phantom gate.

---

## Do-now simplifications (slice 1, /v2 module)

1. **Reset module path to `/v2`** and repoint all imports
   (`via.go`, `example/counter`, `via_test.go`, `signals_test.go`, the `h`
   import). Confirmed: experiment go.mod is byte-identical to main
   (`module github.com/go-via/via`). Release blocker.
2. **Delete `dirty`/`markDirty` ONLY; KEEP `initial`/`setInitial`.**
   `Signal.Set` drops the `markDirty` call. `writeSignalsAttr` keeps reading
   `initial`. Strike IMPL_SPEC step 4 (JSON `{s0:6}` mandate) + test item
   #3; add a negative regression: slice 1 emits no `application/json`
   signal-patch. (Corrects D5 / original do-now #2.)
3. **`Register[T,PT interface{*T;viewer}]`**; bind `pv := PT(&inst)`; delete
   both 500 branches.
4. **CSRF/origin floor + body cap + panic recover** in `POST /_via/a/{n}`:
   `Sec-Fetch-Site`/`Origin` same-origin (fail closed), `MaxBytesReader`,
   stop ignoring the decode error (400/413), `recover`→500, plus
   `WithTrustedOrigin`/`WithInsecureOrigin`.
5. **Default security headers + nonce'd CSP** on GET page and POST patch
   (`nosniff`, `frame-ancestors 'self'`, `object-src 'none'`,
   `base-uri 'self'`, per-render script nonce). Port `genCSPNonce`.
6. **Attr-name allowlist** in `h.RawAttr`/`h.Data` (`[A-Za-z][A-Za-z0-9-]*`)
   — confirmed: `rawAttr.render` escapes only `val`, writes `name` raw.
7. **Convention align + guarantee lint + `-race` CI**: testify,
   `TestSubject_behavior`, tables, `t.Parallel`; a `go/ast` lint that fails
   on `&` in `example/` and on a `func` literal passed to
   `OnClick`/`Register`/`Embed` (doc it as interim lint); FILE the
   type-level closure-ban follow-up as the real guarantee.
8. **Fix DESIGN.md / IMPL_SPEC doc bugs**: actions dash→colon
   (`data-on-click`→`data-on:click`, confirmed code already emits colon);
   first-paint `data-on-load` on a div → head `data-init=@get('/_via/sse')`;
   delete bare `h.H1(c.Count)` headline, lead with `.Display()`; amend the
   "95% precomputed" claim to signal-bind + `h.Static`; **explicitly amend
   "Out of core" to say in-process fan-out lives in a blessed
   `via/topic` sub-package (not core), durability/multipod stay out**
   (resolves D3 scope tension at the doc + structure level, not just prose).

---

## Sequenced slices

| # | Slice | Gated on / ratifies | Effort | Risk |
|---|-------|--------------------|--------|------|
| 1 | Hardened stateless counter on /v2 (all do-now) | — | L | med |
| — | **K1 ratification** (structural descent algo) | before 2 | — | — |
| 2 | Structural-path identity for slots & actions; delete `shapeMatches`/`order`; `Each` Keyed contract specced | K1 | L | med |
| 3 | Reactive handles: `.Display()`, `Signal` round-trip (hydrate response from request, fix `in=nil` zero-reset), `Update`/`Op` mutators, fix `Bind()` slot, `Local[T]` | 2 | L | med |
| — | **K2 + K3 + K4 ratification** | before 4/8 | — | — |
| 4 | Live islands transport: one multiplexed per-tab SSE; head `data-init`; per-island-id idempotent connect; composition-derived stable island id; reconnect floor; **build `via/v2/vtbrowser` module + SSE httptest harness (K4)**; first-paint transient island instance (K2) | 3, K2, K4 | XL | high |
| 5 | `State[T]` + ctx-free-View server reads; panic-on-no-island guard (K2 contract); always-dynamic State slot under caching | 4 | L | high |
| 6 | `via/topic` `Topic[T]` broker + core `ctx.Subscribe`/`Tick`/`OnDispose` | 5 | L | high |
| 7 | `If`/`When`/`Each`/`EachIndexed` with Keyed contract; per-row action rides a Signal; vet analyzer; freeze gate (port pollView+qaView) | 2, 6 | L | high |
| 8 | `Embed` + multi-island; same-type discriminator (K3) | 7, K3 | L | med |
| 9 | `via/sess` (generics type-token, no reflect) + `via/router` (typed `Param[T]`) + core `RenderState[T]`; via_tab CSRF returns | 8 | L | med |
| 10 | Static-skeleton caching (`h.Static` + cached per-type action table; kill POST double-render) | 9 | M | med |

CSRF sequencing note (closes the critic's gap): islands ship in slices 4–8
with **only the stateless same-origin floor** as their action authenticity
guard. The via_tab token (the "primary stateful primitive") does not exist
until `via/sess` lands in slice 9. This is **acknowledged, not hidden**: the
origin floor is a real CSRF defense for the browser-POST threat, and live
islands in 4–8 carry no sensitive multi-tenant auth yet (the auth'd
mini-app is slice 9's example). Document in slice 4 that island actions are
origin-guarded-only until slice 9; do not present the floor as the
permanent stateful auth model.

---

## Example set (the proof ladder)

| Example | Proves | Forces |
|---------|--------|--------|
| counter (hardened) | by-value Register, zero `&`, colon syntax, element-patch, origin floor + CSP | slice 1 |
| form (Signal + Bind + If) | Signal round-trip (no `in=nil` reset), two-way `Bind` with a non-empty slot, `If` keeps structural keys stable | slices 2–3, If half of 7 |
| clock (one live island) | head `data-init`, Tick, **ctx-free View reads State at first paint without panic (K2)**, element-patch by island id, reconnect floor | slices 4–5 |
| chat (multi-user) | `via/topic` Publish fans to every screen, Subscribe coalescing, OnDispose, Each Keyed; API-sizing spike that gates freezing Topic | slice 6, Each half of 7 |
| dashboard (multi-island Embed) | two same-type islands share ONE SSE (cap + HOL conformance), **distinct stable ids via K3**, no double-connect on morph | slice 8 |
| mini-app (router + sess) | typed `Param[T]`, via_tab CSRF login, `RenderState[T]` error on a non-live page | slice 9 |

---

## Cut / keep

**Cut (confirmed app-land or sibling, never core):** StateApp/event-sourcing,
NATS backplane, cross-pod Broadcast, plugins (picocss/echarts/maplibre),
file uploads, metrics, reflection method-name routing + `via:` tags, the dead
`dirty`/`markDirty` machinery, per-island SSE topology, the bare
`h.H1(c.Count)` promise.

**Keep:** zero-`&` by-value Register/Embed + pointer-receiver method values;
sealed `h.H` tree + `Stringish`; colon event syntax with the dead-dash
regression test; `writeSignalsAttr` apostrophe encoding **and the
`initial`/`setInitial` hydration seed it depends on**; POST→text/html→morph
element-patch as the slice-1 contract; stateless-by-default/live-by-opt-in
spectrum + per-island lifecycle; the 410 guard reframed as an absent-key
backstop; Local/Signal/State taxonomy.

**Return as blessed no-reflection sub-packages:** `via/sess`, `via/router`,
and **`via/topic`** (the relocated fan-out broker). Plus core
`RenderState[T]`.

---

## Release strategy

Parallel `github.com/go-via/via/v2` in the same repo (`/v2` go.mod + import
path), main frozen read-only at v0.7.0 as the migration reference. Cut over
per-app — there is no field-by-field port (`View(ctx *CtxR)` → pure
`View()` is a hand rewrite under any option). Keep `h/` in-repo for
cross-version cherry-picks. Gate any v2 app facing a real browser on slice
1's origin floor + CSP.

**Migration deliverable (closes the critic's last gap):** produce a one-page
mapping table as a real artifact in slice 9's docs, not just prose —
`Read→Get`, `Write→Set`, `Bind→Bind`, `Text/Show→Display`,
`View(ctx *CtxR)→View()`, `on.Click→via.OnClick`, `StateTab/Sess/App→
Signal/State/Local + via/sess`. The mini-app (slice 9) doubles as the first
fully-worked screen the table is validated against; viashowcase is NOT
ported as part of 1.0 — it stays on frozen v0.7.0 as the reference, and its
port is an explicit post-1.0 effort, unestimated here and flagged as such.

---

## Residual risks (honest)

1. **K1 may not close under dynamic shape.** If false-`If`-branch descent
   or `When`-slot reservation cannot be made position-independent without
   render-order counting, the structural-key keystone fails and slices
   2/3/7/8/10 are unscopeable. This is the single highest risk; ratify K1
   with a working renderer prototype before slice 2.
2. **The browser tier is now a real M+M build (K4), not free.** If that
   effort slips, the merge gate must NOT be declared — a phantom gate is
   worse than an honest "browser-untested" label.
3. **Topic in a sub-package may feel second-class** for the headline
   multi-user demo. Accepted: it keeps core's "owns no shared state"
   invariant CI-testable, and the chat example proves the one-line ergonomics
   are unaffected (`ctx.Subscribe` stays in core).
4. **Slices 4–8 ship with origin-floor-only CSRF.** Acceptable because those
   islands carry no multi-tenant auth until slice 9; must be documented, not
   silent.
5. **viashowcase port is unscoped.** The 1.0 proof is the 6 greenfield
   examples + the mini-app, not the flagship; that is a deliberate scope
   call to flag to the maintainer.