# Critique Council — Via Framework (whole-surface adversarial review)

Living record of a 3-seat adversarial panel pressure-testing **every feature of Via**
across DX, ease-of-use, and framework design (good taste + industry best practices).
The chair (Claude) convenes the panel each tick, grounds every claim in the real code +
[CONVENTIONS.md](./CONVENTIONS.md), records the debate, and — once the panel converges on a
concrete improvement — applies it (behavior changes via the TDD skill). The loop stops only
when the panel has **fully exhausted** improvement opportunities across all three axes.

## The panel (3 standing seats)

- **UX** — UI/UX specialist, deeply experienced with Datastar (signals, SSE patches, reactivity ergonomics).
- **ARCH** — Senior Go systems architect (type-system, API surface, concurrency, idioms, taste).
- **WEB** — Industry-leading webdev generalist (DX vs peer frameworks: HTMX, Phoenix LiveView, Rails/Hotwire, React/Next, Svelte).

## Conventions

- Each finding gets a stable ID: `T<tick>-<seat>-<n>`.
- Status: `OPEN` → `DEBATING` → `CONVERGED` (panel agrees on fix) → `APPLIED` / `REJECTED` / `DEFERRED`.
- A finding only becomes `APPLIED` after the fix lands (tests green). Behavior changes go through TDD.
- Every load-bearing claim is validated against the codebase before it drives a change.
- Cadence: self-paced loop, each tick fires 60s after the last completes.

## Feature queue (one specific area per tick; revisit as debate demands)

1. `h` HTML builder API & attribute constructors  ← **tick 1**
2. `on` event-handler API & action wiring
3. Signals + Datastar reactivity integration
4. State model (StateApp / StateSess / event-log backplane)
5. Sessions, `via_tab`, CSRF model
6. SSE streaming, heartbeat, reconnect/recovery
7. Forms & validation
8. Middleware stack
9. Plugins API (echarts / maplibre / picocss)
10. Composition & rendering pipeline
11. `vt` test harness (testing DX)
12. Config & server bootstrap
13. picocss integration / styling story
14. push / broadcast / multi-tab fan-out
15. Cross-cutting: error surfaces, godoc, examples, getting-started friction

---

## Tick 1 — area: `h` HTML builder API

Panel convened (UX, ARCH, WEB), each grounded in `h/` + CONVENTIONS + real views.

### APPLIED this tick (3/3-seat convergence, pure-correctness, no behavior change)
- `T1-ALL-1` **APPLIED** — godoc bugs in `h/`. All three seats independently flagged: (a) `elements.go:7` package doc cross-ref `[S]` (strikethrough `<s>`) → should be `[T]` text alias; (b) generator stamped a 2nd contradictory godoc line that *orphaned* the real doc (godoc only renders the contiguous block above the func) on `Style` (attributes.go), `Title`, `Tag`, `VoidTag` (elements.go) — incl. literal nonsense "renders the HTML <> element." Deleted the generated lines, fixed the cross-ref. `go build`+`vet` green.

### APPLIED tick 2 (via tdd-rygba, full RYGBA cycle, all modules `-race` green)
- `T1-WEB-1`+`T1-UX-1` **APPLIED** — added `Aria(name,value)` + shorthands `Alt/Width/Height/Target/Action/Method/AutoComplete/TabIndex/ColSpan/RowSpan` (h/attributes.go). Showcase can drop the 33× stringly-typed `h.Attr("aria-…")`.
- `T1-UX-2` **APPLIED** — `Signal.Text()` now returns an attachable `data-text="$key"` attribute (was a forced `<span>`); new `Signal.TextSpan()` keeps the standalone span. Updated 5 example callers → `TextSpan()`; `room_join.go:148` kept `.Text()` (already in a span — strictly cleaner now). Behavioral guard tests in signal_test.go.
- `T1-ARCH-2` **APPLIED** — added `numeric` constraint + generic `AttrNum[T]` and `ValueNum/MinNum/MaxNum/StepNum`; existing string `Value/Min/Max/Step` retained. Audit confirmed `%v` float formatting yields valid HTML attr values.

### DEBATING (carry to later ticks — design calls, not yet consensus)
- `T1-ARCH-1`/`T1-UX-?` **DEBATING** — *attributes silently dropped in non-element contexts* (group.go:21). ARCH wants fail-loud panic per "Panic on Invalid" doctrine; risk = false positives on legit `Fragment` bubbling (group.go:266). Needs a focused tick.
- `T1-UX-3`+`T1-WEB-3` **DEBATING** — *stringly-typed datastar expressions* (`DataShow/DataOnClick` raw `Printf`-into-JS, datastar.go:8-40); injection surface + rename-fragile signal refs. Proposed: typed predicate/expr helpers on `Signal[T]` (`Eq`, `BindRef`). Large surface — debate scope before building.
- `T1-UX-4`+`T1-WEB-2` **DEBATING** — *no keyed-list/morph-stability* for `Each` (group.go:108); proposed `EachKeyed`. Architectural; defer until state/SSE ticks inform it.
- `T1-WEB-4` **OPEN** — `AttrIf(cond,name,val)` to unify conditional attributes with `IfStr`.
- `T1-ALL-good` — NOTED do-not-touch: pre-escape-at-construction safety/perf, unexported `attribute.isAttr()` marker + sole `RawAttr` escape hatch, `Class/Styles/ClassMap` skip-empty, `Switch/Case` compile-checked branching, `on.Click` bound-method binding, `Static` chrome.

---

## Tick 2 — area: `on` event-handler API & action wiring

Panel convened (UX, ARCH, WEB), grounded in `on/`, `walker.go`, `composition.go`,
`action.go`, `internal/spec`, and real views (counter/chat/todos/sysmon/showcase).

### VALIDATED this tick (chair checked against code)
- `T2-ARCH-1` **VALIDATED / DEBATING** — *action routing is bare-name & root-only; doc
  claims qualified routing that isn't built for actions.* `qualify(prefix,name)` IS
  used for signal/state wire keys (walker.go:54-84) but `actionByName` is keyed by bare
  `m.Name` over **root** `ptrTyp.NumMethod()` only (composition.go:160-172); `handleAction`
  looks up the raw path segment (action.go:120). ∴ `on.Click(child.Method)` typechecks,
  renders `@post('/_action/Method')`, 404s with no diagnostic. The `{cmpID}.{methodName}`
  comment (action.go:60-61) describes routing that exists for signals, not actions.
  - **REJECTED sub-claim**: "two same-named action methods on one root silently collide" —
    Go forbids duplicate method names on one type; impossible.
  - **Design call for next tick**: (a) implement qualified action routing to match signals
    (reuse `qualify` + `fieldPath`), or (b) commit to root-only + **panic at Mount** when a
    child composition declares action methods (per "Panic on Invalid Registration") + fix the
    comment. (b) is smaller & convention-aligned. → focused TDD tick.

### CONVERGED — queued for TDD (additive, fit closed-Option model + Datastar primitives)
- `T2-ALL-1` **CONVERGED (3/3 seats, top pick of UX & WEB)** — *no pending/loading state*.
  Every action POSTs with no `data-indicator`/`aria-busy`/disable-while-inflight; slow links
  look dead + allow double-submit. Add `on.Indicator(sig *via.SignalBool)` (+ maybe
  `on.DisableWhilePending()`); pure `on/` surface, Datastar has the primitive. → TDD.
- `T2-UX-2`+`T2-WEB-5` **CONVERGED** — *event payloads require a SetSignal smuggle* (shared
  signal → rapid cross-row clicks race). Add `on.Value("id", v)` bundling a transient POST
  param read via `ctx.Param("id")`. Bigger lift (touches dispatch). → TDD, after T2-ARCH-1.
- `T2-WEB-3` **CONVERGED** — *`on.Submit` doesn't prevent-default* → races native submit;
  chat/main.go:87 documents the `type=button` workaround, making the named helper a trap.
  Make `on.Submit` prepend `prevent` by default (render already knows the modifier). → TDD.
- `T2-UX-3`+`T2-WEB-4` **CONVERGED** — *missing Datastar modifiers* `.once/.outside/.window`
  (click-away/global shortcuts). Trivial additive Options like `preventFn`/`stopFn`
  (on.go:130). → TDD.
- `T2-UX-4`+`T2-WEB-2b` **CONVERGED** — *`on.Key` is keydown-only, single bare key, no combos*
  (on.go:108). Generalize KeyFilter to Datastar key-modifier chains (`ctrl.enter`) + add
  `on.KeyUp`. → TDD.
- `T2-WEB-2` **CONVERGED** — add `on.Confirm(msg)` (JSON-encoded guard prepended like
  room_host.go:102) for destructive actions. → TDD.

### APPLIED tick 3 (via tdd-rygba; on/ + internal/spec; all modules `-race` green)
- `T2-UX-3`+`T2-WEB-4` **APPLIED** — `on.Once()/Outside()/Window()` modifier options
  (pre-alloc closures like prevent/stop). Verified against bundled datastar.js — the `on`
  plugin reads `mods.has("once"/"outside"/"window")`. Unlocks click-away + global shortcuts.
- `T2-WEB-2` **APPLIED** — `on.Confirm(msg)` — JSON-encoded `confirm(<msg>)&&@post(...)` guard;
  added `Confirm` field to spec.Trigger, render emits it after Pre statements, excluded from
  the bareAttr fast path. Composes with SetSignal (`$x=5;confirm("…")&&@post(…)`) — locked by test.
- `T2-ALL-1` **APPLIED** — `on.Indicator(sig *via.Signal[T])` → `data-indicator="<key>"`.
  Verified datastar value-form syntax (bare key, no `$`). Drives spinners/aria-busy/disable
  while a POST is in flight — the 3/3-seat top gap, now closed.

### REJECTED tick 3 (grounding refuted the finding)
- `T2-WEB-3` **REJECTED** — *"`on.Submit` should prevent-default by default."* Bundled
  datastar.js `on` plugin already auto-prevents: `e instanceof HTMLFormElement && o==="submit"
  && c.preventDefault()`. Adding `.prevent` is redundant. The chat/todos `type=button`
  workaround is about the handler living on the *button* (no auto-prevent) vs the *form* (auto).
  → real fix is a DX/example change: put `on.Submit` on the `<form>`. Queued as doc/example item.
- `T2-ARCH-1` option (b) **REJECTED** — *"panic at Mount when a child composition declares
  action methods."* The canonical `countercomp` example has `CounterCard.Inc(ctx *via.Ctx)` —
  action-shaped, but legitimately forwarded by the parent (`IncA`→`p.A.Inc`), not bound directly.
  A Mount-panic would break Via's own flagship composition example. **Resolution APPLIED**: fixed
  the misleading action.go:60-61 comment (claimed `{cmpID}.{methodName}` routing that doesn't
  exist for actions) to document the real contract — bare-name, root-only registration, children
  forward from a root action. Genuine bug surface remaining = a clearer dispatch-time diagnostic
  when an action name misses the registry (bare 404 today); queued as a safe runtime improvement.

### DEFERRED pending in-browser verification
- `T2-UX-4` **DEFERRED** — KeyUp + key-combo filters. While reading datastar.js the chair
  noticed the `on` plugin parses only window/document/capture/passive/once/outside/prevent/stop/
  debounce/throttle/delay/viewtransition modifiers — **no key-name filter**. This casts doubt on
  whether the EXISTING `on.Key("Enter",…)` → `on:keydown.Enter` actually filters by key in
  datastar (may fire on every keydown). Unit tests only assert the rendered string (vt bypasses
  datastar). Must verify in-browser before building KeyUp/combos on this foundation. → next tick:
  browser-verify on.Key, then decide.

### DEBATING / NOTED
- `T2-UX-5`+`T2-WEB-6` **DEBATING** — action errors are toast/global-only (action.go:233-242);
  no inline field-level feedback. Mechanism exists (write a `SignalStr` from the action);
  resolve as doc + maybe `ctx.FieldError(sig,msg)` helper.
- `T2-ARCH-3` **NOTED** — `-fm` trampoline parse (spec.go:54 `LastIndex(".")`) has a narrow
  canary; generic/embedded receivers (`[...]` instantiation suffixes) untested. Add canary
  cases before any parse hardening.
## Tick 3 (part 2) — area: Signals + Datastar reactivity

Panel convened (UX, ARCH, WEB), grounded in signal.go, shape_*.go, encoding.go,
walker.go, ctx.go, render.go, and the bundled datastar.js.

### VALIDATED this tick (chair checked against code) — queued for dedicated TDD
- `T3-ARCH-1` **VALIDATED — P0 correctness, top priority tick 4.** *`Signal.val` read/written
  without synchronization.* `Update` writes `s.val = next` (signal.go:76) holding no lock; only
  the dirty bit is locked (ctx.go:310-312). `encode()` reads `s.val` (signal.go:152) under
  `queue.mu` on the flush path (render.go:226-241). The "safe from any goroutine" claim
  (ctx.go:303) covers the bit, not the value. `StateSess/StateApp` writes correctly go under
  `queue.mu` (ctx.go:386-394) — `Signal.Update` is the outlier. Race on the documented raw-
  goroutine `Update`+`SyncNow` pattern; `-race` misses it (no test drives concurrent
  Update+flush). **Fix**: write `s.val` under `queue.mu` in Update; document Read as action/View-
  goroutine-only (lock-free). Hot-path concurrency change → dedicated TDD cycle w/ a real
  concurrent race test. → tick 4 first.

  **TICK 4 DEEPENED ANALYSIS — NEEDS MAINTAINER SIGNOFF, not auto-applied.** Chair dug
  further before fixing: the picture is narrower AND the proposed fix is incomplete.
  - Stream/action/lifecycle writes are ALREADY safe — all serialized by per-Ctx
    `actionMu` (stream.go:128, action.go:171, ctx.go:331 SyncNow, runtime.go:325).
    Confirmed by passing `TestStream_doesNotRaceWithConcurrentActions`.
  - The race is ONLY a **raw user goroutine** (launched in OnConnect) calling
    `Write/Update` without `actionMu`, concurrent with an action flush. The Write godoc
    actively invites this pattern → the documented pattern is itself racy.
  - **The `queue.mu`-on-val fix is INCOMPLETE**: `View` reads `s.val` lock-free during
    flush (render.go:256), so a raw write still races the View read. True invariant =
    "every write AND every flush/View serialized by `actionMu`."
  - A single `actionHeld` bool can't auto-detect "inside an action context" (ctx-global →
    a raw goroutine mis-reads another goroutine's action as its own; Go has no cheap
    held-by-current-goroutine check).
  - Resolution options (maintainer picks — public concurrency-contract change):
    (1, recommended) add `ctx.Mutate(fn)` running writes under `actionMu`+flush; document
    bare Write/Update as action-context-only; deprecate raw-Write-then-SyncNow guidance.
    (2) always take actionMu in Write — deadlocks inside actions; rejected.
    (3) document the limitation only; weakest.
  - Status: **DEBATING — flagged to user.** Redesigning the public concurrency contract
    autonomously is hard to reverse; awaiting signoff.

### CONVERGED — queued for TDD (additive / convention-aligned)
- `T3-UX-1`+`T3-WEB-1` **CONVERGED (P0, both seats top/near-top pick)** — *no computed/derived
  signals.* datastar.js ships `data-computed`/`data-effect` (Via embeds it) but exposes neither;
  every derived value (todos pendingCount/visible) forces a server round-trip. Add `via.Computed`
  (+ `Effect`) as typed `h.H` built from `Signal.Key()` so users never hand-type `$`. → TDD.
- `T3-UX-3`+`T3-WEB-2` **CONVERGED (P0/P1)** — *no local client-only (`_`-prefixed) signals.*
  Ephemeral UI (accordion/tab/hover) can't exist without a server round-trip; the `_` convention
  is load-bearing (broadcast.go:38) yet undocumented + untyped. Add `via.Local[T]`/`Signal.Local()`
  emitting a `_`-prefixed signal never bound to a server slot. Ties to [[project_datastar_local_signal_underscore]]
  (must browser-verify `_` inbound behavior). → TDD + browser-verify.
- `T3-UX-collide`+`T3-ARCH` **CONVERGED** — *wire-key collisions silently clobber.* walkStruct
  (walker.go:51-61) appends slots with zero dup detection; `initialSignals` map last-write-wins.
  Violates "Panic on Invalid Registration" (which `checkPathParams` honors, composition.go:194).
  Add a dup-key set in descriptor build → panic at Mount. → TDD (verify no example collides first).
- `T3-UX-4` **CONVERGED** — *`Show` can't negate; no `Class` helper.* Add `Signal.ShowUnless()`
  (`data-show="!$key"`) + `Signal.Class(name)` (`data-class-<name>="$key"`). One-liners like
  Attr/Style; kills the `"!$"+Key()` `$`-juggling fall-off. → TDD.
- `T3-ARCH-2`+`T3-UX-6` **CONVERGED** — *composite signal decode silently dropped.* `encodeScalar`
  handles slice/map via json.Marshal (encoding.go:47) but `decodeScalarInto` (encoding.go:62-116)
  has no Slice/Map/Struct arm → inbound SignalSlice/SignalMap `Bind()` is a client→server no-op.
  Fix: add a json round-trip default arm OR drop `Bind()` promotion on composites + document
  server→client-only. → TDD (characterization first).
- `T3-WEB-5` **CONVERGED** — *`Update` marks dirty even on no-op writes* (signal.go:76). Gate
  `markSignalDirty` behind `next != cur` for comparable T (the Num/Bool/Str shapes). Avoids
  redundant SSE patches; users hand-guard today (todos main.go:155). → TDD.

### APPLIED tick 4 (via tdd-rygba; signal.go + computed.go; all modules `-race` green)
- `T3-UX-4` **APPLIED** — `Signal.ShowUnless()` (`data-show="!$key"`) + `Signal.Class(name)`
  (`data-class-<name>="$key"`). Audit verified against datastar show/class plugins; documented
  the mixed-case class-name footgun (browser folds attr names lower-case) + characterization test.
- `T3-UX-1`+`T3-WEB-1` **APPLIED (the P0 derived-state gap)** — `via.Computed(key, expr)` →
  `data-computed-<key>`, `via.Effect(expr)` → `data-effect`. Unlocks client-side derived values
  (no server round-trip). MVP takes a raw Datastar expr (the primitive); a fully-typed expression
  builder (`sig.Eq`/`sig.Plus`) remains queued as the discoverability follow-up.

### ROLLED TO TICK 5 (queued, not yet started)
- `T3-WEB-5` (no-op dirty gate), `T3-UX-collide` (wire-key collision Mount panic),
  `T3-ARCH-2` (composite signal decode), `T3-UX-3`+`T3-WEB-2` (`via.Local` client-only signals —
  needs design + browser-verify), `T2-UX-4` (browser-verify on.Key filtering). Tick 4 front-loaded
  the race analysis + 2 cycles; these carry forward to keep cycles high-quality over crammed.
- **State-model panel (queue area 4) deferred to tick 5** — convene after the reactivity backlog clears.

### APPLIED tick 5 (via tdd-rygba; composition.go + encoding.go; all modules `-race` green)
- `T3-UX-collide` **APPLIED** — Mount panics on duplicate wire keys across signalSlots+scopeSlots
  (shared data-signals namespace); full suite passing proved no existing example/test collides.
  Audit confirmed no false-positive on nested children (qualify prefixes child keys) or sibling
  same-type children.
- `T3-ARCH-2` **APPLIED** — composite signal decode arm in `decodeScalarInto` (Slice/Map/Struct/
  Array → json round-trip) so inbound SignalSlice/SignalMap reach the action, matching scalar
  parity. Audit hardened it: zero dst before unmarshal so a removed SignalMap key doesn't linger
  (full-replace, not merge). Characterization test pins it.

### ROLLED TO TICK 6
- `T3-WEB-5` (no-op dirty gate) **HELD** — real semantic subtlety: gating no-op writes changes
  whether a client-drifted bound input gets re-synced by an unchanged-value action. Decide deliberately.
- `via.Local` client-only signals + browser-verify (on.Key filtering, Local client-only) → tick 6.

## Tick 6

### APPLIED tick 6 (via tdd-rygba; appval.go; all modules `-race` green)
- `T5-ARCH-2` **APPLIED** — split `applyChange` into `applyChangeL1(c) bool` (gated L1 re-pull,
  reports whether L1 advanced) + a `broadcastRender` gated on that bool. A redelivered/stale hint
  that changes nothing is now a silent no-op, matching reconcileKey/applySessionChange. Internal
  characterization test pins changed=false for stale/dup/unknown-cell; existing L1-gate test preserved.

### REJECTED tick 6 (grounding refuted the finding)
- `T5-WEB-3`+`T5-ARCH-silent` **REJECTED** — *"silent writes should still Append the changes-feed
  hint."* The hint Append is itself a PROPAGATION mechanism — it makes peers re-pull the Store and
  re-render. SyncOff's documented contract (ctx.go:337-362) is "the write lands in the Store but does
  NOT reach any browser this action." Always-appending the hint would re-render other tabs/pods,
  violating SyncOff (try-before-commit / no-partial-leak is the whole point). Durability already
  comes from the CAS to Store (always happens); the hint is fan-out, correctly suppressed. Only real
  gap = doc clarity: make explicit that SyncOff suppresses CROSS-POD propagation too (peers converge
  via a later loud write or configured reconcile). → small doc note, queued.

### APPLIED tick 6 (cont.) — on.Key BUG FIX (via tdd-rygba; on/on.go; all modules `-race` green)
- `T2-UX-4` **APPLIED (real bug, conclusively settled from datastar source)** — datastar v1.0.2 has
  ZERO keyboard-key matching (verified: the `on` plugin checks only window/document/capture/passive/
  once/outside/prevent/stop; the lone `.key` in datastar.js is plugin-registration internals). So
  `on.Key("Enter")` → `on:keydown.Enter` fired the action on EVERY keystroke (chat sent on every key!).
  Fix: KeyFilter is now an `evt.key==='Enter'&&` EXPRESSION guard (datastar exposes the event via
  argNames:["evt"]) instead of the no-op attribute modifier. 4 repo callers unaffected (same API).
  Browser-verify was blocked (port :8931 held by another context's showcase-bin; non-:8931 needs an
  interactive whitelist) — source proof is conclusive; fix follows datastar's documented expr-guard idiom.
- `via.Local` (T3-UX-3/WEB-2) **APPLIED** — client-only `_`-prefixed signal handle (Init/Bind/Text/
  Show/ShowUnless/Class/Toggle/Ref). `_`-never-sent rests on established datastar behavior
  ([[project_datastar_local_signal_underscore]]); object-form `data-signals="{_open:false}"` init.

## Tick 16 (FINAL) — Cross-cutting docs/godoc panel (area 14) + Loop summary

### APPLIED tick 16 (pure-correctness doc/godoc fixes; build+vet+test green, no behavior change)
- `push.go` package doc — 3 bugs: `Signal[T].Set` (no such method) → `[Signal.Write]`; `[Redirect]/[Toast]`
  (don't resolve — they're methods) → `[Ctx.Redirect]/[Ctx.Toast]`; removed the fabricated "sentinel-error
  intents in action.go — those return errors" claim (Toast/Redirect live in push.go and return nothing).
- `docs/reactive-state.md:103` — `s.Text()` comment said `<span data-text>` but Text() returns an
  attachable attribute; fixed + added the `s.TextSpan()` standalone-span line.
- `README.md:138` — plugin list `picocss, echarts` → added `maplibre` (shipped + documented + has the maps example).
- `docs/examples.md` — "Twelve" → "Thirteen" runnable examples; chat row said `StateAppSlice` but the example
  uses `StateAppEvents`+`Fold` → corrected, and dropped the now-inaccurate "the tutorial builds it" link.
- `stateappevents_migrate.go:30` — backticked `reflect.TypeFor[OldV]()` so `[OldV]` stops rendering as a dead godoc link.

### QUEUED (cross-cutting, bigger)
- `T16-tutorial-mismatch` **VALIDATED (WEB, biggest learning-path defect)** — docs/tutorial.md teaches
  `StateAppSlice` + manual `Update`-trim and says "the finished app is internal/examples/chat", but chat
  actually uses `StateAppEvents[Posted,[]Message]`+`Fold` and a comment that argues AGAINST the trim pattern.
  Rewrite the tutorial onto StateAppEvents (or point it at a StateAppSlice example). → docs rewrite.
- `T16-text-naming` — `Signal.Text()` (attribute) vs `TextSpan()` (span) names don't telegraph the difference
  (the maintainer's own doc conflated them); consider `TextAttr()` or prominent getting-started coverage.
- `T16-boot-idiom` — README/tutorial quickstarts show `http.ListenAndServe(app)` (valid, shows embeddability)
  while getting-started/production now use `app.Start()`; add a one-line note on when to use which.
- `T16-good` — godoc accuracy across via/h/on/sess/mw/vt/plugins is otherwise high; op-tables, vt API,
  lifecycle API, h-helpers all verified to match code. Plugin `Plugin()` ctor naming uniform.

---

## Loop summary (final synthesis)

16 ticks of 3-seat adversarial review (UX/datastar · ARCH/Go-systems · WEB/peer-frameworks) across every
feature area. ~32 fixes applied (each via the TDD RYGBA cycle, all modules green under `-race`); the
high-impact findings are queued with precise fixes or flagged for signoff (security-gate / concurrency /
client-behavior changes not made autonomously). Grounding refuted 4 plausible findings (on.Submit prevent,
action-scoping Mount-panic, silent-write hint, on.Key "duplicate collision") — recorded as REJECTED.

### (a) APPLIED, by area
- **h (HTML builder):** godoc bug fixes (`[S]`→`[T]`, stripped generated/contradictory doc lines on
  Style/Title/Tag/VoidTag); `Aria` + 10 attr shorthands (Alt/Width/Height/Target/Action/Method/AutoComplete/
  TabIndex/ColSpan/RowSpan); `numeric` constraint + `AttrNum[T]` + ValueNum/MinNum/MaxNum/StepNum;
  `Pattern`/`MinLength`/`MaxLength`.
- **Signals & reactivity:** `Signal.Text()`→attachable attribute + `TextSpan()`; `ShowUnless`; `Class(name)`;
  `via.Computed`/`via.Effect`; `via.Local` client-only signals; composite SignalSlice/SignalMap decode;
  duplicate-wire-key Mount panic.
- **on (events):** `Once`/`Outside`/`Window` modifiers; `Confirm`; `Indicator`; **on.Key key-filter BUG FIX**
  (datastar has no key modifier → was firing on every keystroke → now an `evt.key===` guard).
- **State:** `applyChange` broadcast gated on actual L1 advance (no stale-hint render storm).
- **Sessions/CSRF:** session-mismatch 403 observability (metric + log); `WithSessionCookieName`;
  `touchSession` on the SSE heartbeat (live tab's session can't expire under it).
- **SSE:** `X-Accel-Buffering: no` (nginx); per-write deadline re-arm in drain.
- **Forms:** multipart text no longer JSON-coerced (target-type decode); `via.Files` multi-file handle.
- **Middleware:** `statusWriter.Unwrap()` (no Hijacker/ReaderFrom loss); `via.RouteFrom(r)`.
- **Plugins:** duplicate `RegisterAppSignal` Mount panic; picocss `Vary: Accept-Encoding` + per-encoding ETag.
- **Rendering:** capacity gate before OnInit; renderFragment Render-error check; composition doc `View(*CtxR)`.
- **vt harness:** `TabIDFromHTML`; `SSE` cancel via `t.Cleanup` (leak-safe); `WithSignal` `_`-prefix warning;
  testing.md broken example + "what vt does NOT simulate".
- **Config/bootstrap:** `App.Run() error`; `config.validate()` (negative-option panic); docs → Start/Run.
- **Push/broadcast:** `App.BroadcastToast` (XSS-safe site-wide notice).
- **Cross-cutting docs:** push.go godoc, reactive-state, README, examples.md, migrate backtick.

### (b) SIGNOFF BACKLOG, by theme (parked — needs a maintainer decision)
1. **Concurrency-contract family** — one decision (recommend `ctx.Mutate(fn)` + actionMu) clears both:
   `T3-ARCH-1` (Signal.val written unlocked vs flush-encode read) + `T12-ARCH-resync-race` (SSE reconnect
   resync renders outside actionMu). Both are real `-race`/torn-read holes on raw-goroutine + reconnect paths.
2. **Graceful-deploy family** — one design clears three: `T8-ALL-1` (client re-arm on clean SSE close —
   datastar stops retrying → frozen tab on deploy), `T14-ARCH-shutdown-deadline` (WithShutdownTimeout doesn't
   bound OnDispose/backplane.Close → SIGTERM hangs forever), `T14-WEB-graceful-deploy` (push `ctx.Reload()`
   to tabs at top of Shutdown + `/healthz`+`/readyz` drain flag).
3. **Security family** — `T5-ARCH-sid` (raw session id = bearer secret on the shared backplane → HMAC it),
   `T6-ALL-1` (session-mismatch 403 dead-end → route through recoverSSE; the localhost-clobber freeze),
   `T6-ARCH-fixation` (`__Host-` cookie default + adoption/rotate-on-auth).
4. **Plugin CSP** — `T11-ALL-1`: plugin init scripts (head + Mount) carry no nonce → `mw.CSP()` blocks them →
   charts/maps silently never init (maplibre docs even advertise CSP). Thread the doc nonce into render.
5. **Auth/routing** — `T10-ALL-1` (requireAuth 303 is dead on action/SSE fetches → `mw.RequireRedirect` SSE
   redirect; the SHIPPED auth example + showcase + docs teach the broken pattern), `T10-ARCH-placement`
   (group MW runs after body-parse/ctx-lookup/OnInit → unauth work before the guard).
6. **Scale/backpressure** — `T5-ARCH-1`/`T15-ARCH-storm` (broadcastRender goroutine-per-tab render storm +
   render-order inversion → move render into the drain path), `T8-ARCH-backpressure` (cap per-tab elements/
   scripts queue), `T9-ARCH-upload` (multipart mem-cap==wire-cap → ~32MiB heap/upload DoS + coupled temp-file
   cleanup), `T14-ARCH-writetimeout` (WriteTimeout=0 leaves action POSTs without a write deadline).
7. **Big-DX roadmap** — `h.Region` granular re-render (whole-View re-renders today); per-page `Head()`/title/
   OG (one global title — SEO-disqualifying); `via.Errors` form-field primitive + live validation;
   `EachKeyed`/`data-for` keyed lists; typed datastar expression builders (kill stringly-typed `$`);
   cluster-aware + topic-addressable broadcast (`T15-WEB-clusterpush` + BroadcastWhere/topics); presence;
   env-config (`via.FromEnv`); first-class TLS (`WithTLS`); `SessionStore` (restart = global logout today);
   vt DOM/`Click(selector)` binding-aware seam.

### (c) RECOMMENDED next-actions order
1. **Concurrency-contract** (#1) — correctness; one `ctx.Mutate` decision, then I can land both via TDD.
2. **Security** (#3) — sid-HMAC is no-public-API + high-severity; session-403 recovery fixes the daily-hit freeze.
3. **Graceful-deploy** (#2) — one design clears 3 items; unblocks safe rolling deploys.
4. **requireAuth redirect** (#5, T10-ALL-1) — the shipped auth example is broken on session expiry; small once decided.
5. **Plugin CSP** (#4) — makes plugins usable under the framework's own CSP.
6. **Scale/backpressure** (#6) — multipart mem-DoS first (security-adjacent), then the render storm.
7. **Big-DX roadmap** (#7) — sequence as deliberate initiatives (Region + Head + Errors are the headline DX gaps).

Loop stopped: every feature area has been adversarially reviewed; remaining opportunities are applied,
queued with precise guidance, or flagged for signoff. Nothing committed — all work is on
`chore/adversarial-critique-loop` awaiting review.

## Tick 15 — cfg.validate + BroadcastToast + Push/broadcast panel (area 13)

### APPLIED tick 15 (via tdd-rygba; all modules `-race` green)
- `T14-ARCH-validate` **APPLIED** — `config.validate()` invoked at New panics on negative
  WithShutdownTimeout/WithMaxRequestBody/WithMaxUploadSize/WithMaxContexts (0 stays valid =
  unlimited/default). Suite passing confirms no example trips it.
- `T15-UX-broadcasttoast` **APPLIED** — `App.BroadcastToast(message)` — the XSS-safe site-wide-notice path
  (reuses the JSON-encoded toast snippet via an extracted `buildToastScript`), so apps stop hand-building
  toast JS (the showcase's 40-line raw-JS banner). Documented as best-effort + single-pod (shares Broadcast's
  semantics).

### VALIDATED — queued / FLAG (scale & cluster)
- `T15-WEB-clusterpush` **VALIDATED, FLAG (WEB P0)** — imperative `Broadcast`/`BroadcastSignals`/`BroadcastToast`
  fan out only over the LOCAL pod's contextRegistry, never the backplane — while STATE fan-out crosses pods.
  So a background job's Broadcast reaches only one pod's tabs behind a LB (silent partial delivery). Fix: route
  imperative pushes through a backplane control stream (`__via_push` envelope + per-pod projector replay).
  Architectural → signoff/roadmap.
- `T15-ALL-topics` **VALIDATED (UX+WEB P0/P1)** — no topic/room/user targeting; broadcast is all-or-nothing,
  so the showcase smuggles room-filtering into a client-side JS string (room_host.go:104) that ships to + runs
  on EVERY tab. `broadcastRender` already has `sess`+`subscribed` filtering internally — a `BroadcastToSession`/
  `BroadcastWhere(pred)` exposes it (smaller win); full server-side topics + `ctx.Subscribe`/`BroadcastTo` is the
  larger fix. → queue (start with BroadcastWhere).
- `T15-ARCH-storm` **RE-CONFIRMED [[T5-ARCH-1]]** — broadcastRender's `go c.SyncNow()` per tab per write is an
  unbounded render storm AND (new) an unordered-render last-wins inversion (two goroutines for one ctx race
  autoElements ordering). Fix: move render into the drain path (set dirty + notify; SSE loop renders once).
- `T15-ARCH-backpressure` **RE-CONFIRMED [[T8-ARCH-backpressure]]** — per-tab elements/scripts grow unbounded
  for a slow/stalled tab (signals capped, these aren't). Cap + drop-to-reload on overflow.
- `T15-UX-silent` **VALIDATED** — push primitives silently no-op on nil queue / swallowed Toast marshal error;
  route through logErr (debug) so dead pushes are observable. → small TDD.

### ROADMAP / docs
- `T15-WEB-outside` — no app-level off-request push for signals/toast beyond raw Broadcast; add `ToastAll`/
  `ToastTo(topic)` (now partly done via BroadcastToast). `T15-WEB-presence` — no presence primitive (showcase
  hand-rolls StateApp[map]); `via/presence` on topics+disconnect hook. `T15-WEB-delivery` — document the
  at-most-once/no-replay contract on imperative push (vs durable State).
- `T15-UX-patch-discoverability` — cross-ref Patch godoc → StateAppEvents as the preferred reactive path.
- `T15-ALL-good` — do-not-touch: State fan-out (backplane-durable, cross-pod, CAS-correct, cursor-replay) =
  the crown jewel; `subscribed(key)` dependency-tracked re-render (auto-subscribe, no manual pub/sub); Toast
  XSS hardening + Redirect open-redirect defense; single-drainer queue with clean map-ownership handoff;
  autoElements replace-vs-elements-append coalescing; BroadcastSignals per-ctx isolation (no cross-tab leak).

## Tick 14 — WithSignal warning, Run(), docs + Config/bootstrap panel (area 12)

### APPLIED tick 14 (via tdd-rygba; all modules `-race` green)
- `T13-ALL-falseconfidence` (cheap win) **APPLIED** — `vt.WithSignal` now logs a non-fatal warning when
  given a `_`-prefixed (client-only) signal name, catching the local-signal-gotcha class at test-write time.
- `T14-ARCH-run` **APPLIED** — added `App.Run() error` (returns the bind error; normalizes ErrServerClosed
  to nil); `Start()` is now the panic-on-error wrapper over it. A bind failure (EADDRINUSE) is a runtime/IO
  error that callers can handle, per CONVENTIONS. Test forces a taken port → Run returns error.
- `T14-docs` **APPLIED** — getting-started.md + production.md now use `app.Start()`/`app.Run()` (graceful
  shutdown wiring) instead of raw `http.ListenAndServe(app)`, which bypassed SIGTERM drain/OnDispose/
  backplane-close (the deploy-freeze enabler). Fixed the now-unused net/http import + preserved `:8080` via WithAddr.

### VALIDATED — FLAG TO USER (production-safety, deploy-freeze family)
- `T14-ARCH-shutdown-deadline` **VALIDATED, FLAG** — `WithShutdownTimeout` only bounds `srv.Shutdown` (the
  HTTP drain). Steps 3-5 — per-Ctx `disposeCtx`→user `OnDispose` under actionMu (runtime.go:325), sweeper
  close, `backplane.Close()` — run with NO deadline. A wedged OnDispose or a blocking backplane.Close hangs
  SIGTERM **forever**, defeating the timeout and causing the exact deploy-freeze this codebase fights. Fix:
  run steps 3+ under the same ctx deadline (dispose in a goroutine selected on ctx.Done, time-box
  backplane.Close, log overruns). Touches teardown concurrency → signoff (groups with the graceful-deploy family).

### CONVERGED / ROADMAP
- `T14-WEB-graceful-deploy` **CONVERGED (ties [[T8-ALL-1]])** — no /healthz/readyz; and `Shutdown` closes SSE
  streams cleanly without first pushing a client re-arm → datastar sees a clean close, stops retrying →
  frozen tab on rolling deploy. High-value fix: at the TOP of Shutdown, `ctx.Reload()` every live tab
  (machinery exists, push.go) + a readiness flag that 503s /readyz during drain. → bundle with T8-ALL-1 signoff.
- `T14-WEB-env` **ROADMAP (WEB P0)** — no env-var/12-factor config; `$PORT` ignored (fatal on Cloud Run/Heroku).
  Add a `via.FromEnv()` Option mapping VIA_ADDR/PORT/VIA_LOG_LEVEL/etc. Additive, fits functional-options.
- `T14-ALL-tls` **CONVERGED (UX+ARCH+WEB P1)** — no first-class TLS: `Start`/`Run` only call ListenAndServe,
  so a TLSConfig set via WithHTTPServer is silently served as plaintext. Add `WithTLS(cert,key)` + branch
  Run to ListenAndServeTLS (keeps graceful shutdown). → TDD.
- `T14-ARCH-writetimeout` **VALIDATED** — `http.Server.WriteTimeout` defaults 0 (SSE-safe) but that leaves
  EVERY action POST/page render with no write deadline (slow-read pins a goroutine); ReadTimeout=0 too
  (body-slowloris). Fix: per-request write deadline on non-SSE handlers via ResponseController +
  `WithActionWriteTimeout`. → TDD.
- `T14-ARCH-validate` **VALIDATED** — no cross-option/bounds validation: negative WithShutdownTimeout
  (→ instant ungraceful kill), negative size caps silently accepted. Add a `cfg.validate()` panic pass at
  New (convention-aligned). → TDD.
- `T14-UX-idletimeout` **NOTED** — `WithIdleTimeout(0)` can't disable (cmp.Or treats 0 as unset → 120s back);
  inconsistent with WriteTimeout. Use a set-bool or document. 
- `T14-WEB-metrics-adapter` **ROADMAP** — ship a `viaprom`/`viaotel` Metrics adapter (mirror vianats) so
  teams stop reimplementing the 3-method forwarder.
- `T14-ALL-good` — do-not-touch: tryRegisterCtx cap+register fused (TOCTOU-safe), serverMu guards
  server/Use, backplaneDone-closed-before-Close (graceful-vs-drop distinction), ReadHeaderTimeout 10s/
  IdleTimeout 120s/MaxHeaderBytes 1MiB slow-loris defaults, verifyMethodNameTrampoline fail-fast at New,
  HTTPServer escape hatch + App-is-http.Handler embeddability, secure-cookie-default + conflict panic.

## Tick 13 — rendering fixes + vt-harness panel (area 11)

### APPLIED tick 13 (via tdd-rygba; all modules `-race` green)
- `T12-ARCH-oninit-capacity` **APPLIED** — moved the `tryRegisterCtx` capacity gate BEFORE OnInit, so an
  over-capacity (503-bound) request no longer runs user OnInit work. Test asserts OnInit doesn't run at
  WithMaxContexts(1) capacity.
- `T12-ARCH-frag-err` **APPLIED (defensive)** — renderFragment now checks the h.Render error, logs it, and
  returns "" (so the empty-frag guard preserves the last good frame) — consistent with the page path.
  Currently unreachable (strings.Builder never errors); future-proofing.
- `T13-UX-tabid` **APPLIED** — exported `vt.TabIDFromHTML(html)` (wraps the internal extractor) to kill the
  4 hand-rolled `via_tab` regexes duplicated across tests.
- `T13-ARCH-sse-leak` **APPLIED** — `vt.Client.SSE` now registers its `cancel` with `t.Cleanup`, so a
  test that `t.Fatal`s before its `defer cancel()` no longer leaks the reader goroutine + connection.
- `T13-docs` **APPLIED** — fixed docs/testing.md broken example (`via.WithTestServer` → `vt.Serve`) and
  added a "What vt does NOT simulate" section (no Datastar JS → `_`-signals get sent, no client key-filter/
  debounce/bind coercion, raw-string frame matching) — closing the P0 false-confidence gap cheaply.

## Tick 13 (part 2) — area: vt test harness

Panel convened (UX, ARCH, WEB), grounded in vt/vt.go + real tests + docs/testing.md.

### CONVERGED — headline (3/3 seats P0) — partially addressed
- `T13-ALL-falseconfidence` **CONVERGED, doc-addressed** — vt doesn't run Datastar → a green test ≠ working
  browser; this is how the on.Key key-filter bug + the `_`-signal gotcha shipped. Docs caveat APPLIED;
  remaining cheap win: a `WithSignal` warning when given a `_`-prefixed name (a real browser never sends
  it). → small TDD.

### VALIDATED — queued (DX, meaty)
- `T13-WEB-dom`+`T13-WEB-binding` **VALIDATED (WEB P0 top pick)** — `Action(name)` fires by action name and
  NEVER inspects the rendered view, so a test passes even if the `on.Click` binding is deleted; and all
  assertions are raw-string `Contains`, not DOM. Propose a `x/net/html` seam: `tc.Click(selector)`/
  `tc.Submit(form)` that discover the bound action from the element's `data-on-*` attr + `tc.Find(selector)`
  structural assertions. Large new API → roadmap (biggest testing-DX gap vs LiveViewTest/RTL).
- `T13-ALL-awaitframe` **VALIDATED** — `AwaitFrame` accumulates raw SSE bytes + substring-matches, never
  resetting per event → a needle from a stale frame satisfies a later assertion (tautological), and `>8<`
  matches `>18<`. Parse the stream into `event:`/`data:` records; offer per-frame + negative (`NoPatch`)
  assertions. → TDD (additive, keep old AwaitFrame).
- `T13-ARCH-sseready-compress` **VALIDATED** — `SSEReady` waits for the `: ready` marker, which sse.go
  suppresses under Content-Encoding → SSEReady blocks the full timeout then fatals behind any compressing
  middleware. Guarantee/assert no Accept-Encoding + document. → TDD.
- `T13-UX-reconnect` **VALIDATED** — no `tc.Reconnect()` / `vt.RawSSE` primitive; reconnect tests (Via's
  riskiest behavior) bypass vt with a 30-line raw `openRawSSE` re-impl. Add first-class reconnect helpers. → TDD.
- `T13-UX-signals-read`+`T13-ARCH-fire-body` **NOTED** — no signal-state read seam (`tc.Signals()`), and
  `Fire()` discards the response body (can't assert action error frames); add `FireR()` returning status+body.

### NOTED / roadmap
- `T13-WEB-e2e` — no real-browser E2E guidance; add a chromedp/Playwright-against-vt.Serve recipe to testing.md.
- `T13-ARCH-wire-divergence` — `WithSignal` encodes JSON-number on the JSON path but a string on the
  multipart path; document or normalize.
- `T13-UX-reload-footgun` — `Reload()` silently mints a NEW tab id; escalate the godoc caveat into the API doc.
- `T13-ARCH-tabid-lock` — `Client.tabID` read unlocked in Action/SSE but mutated under lock in Reload; lock
  consistently or document Client as single-goroutine.
- `T13-ALL-good` — do-not-touch: typed `Action(p.Method)` compile-time typo protection, `SSEReady` handshake
  (vs sleep), `Fork` shared-jar multi-tab, isolated per-client http.Transport (+ canary test), full
  middleware/session/SSE stack end-to-end (no fake seam).

## Tick 12 — app-signal panic + Composition/rendering panel (area 10)

### APPLIED tick 12 (via tdd-rygba; all modules `-race` green)
- `T11-ARCH-signal-collision` **APPLIED** — `RegisterAppSignal` now panics at registration on a
  duplicate key (was silent last-write-wins). Full suite passing confirms no existing plugin/example
  collides. Convention-aligned twin of the wire-key + route collision panics.
- `T12-UX-docbug` **APPLIED** — fixed composition.go:5 package-doc prose (`View(*Ctx)` → `View(*CtxR)`,
  the actual read-only render-context signature; the example at :15 was already correct).

### VALIDATED — FLAG TO USER (concurrency; groups with the parked race family)
- `T12-ARCH-resync-race` **VALIDATED, FLAG** — *the SSE reconnect resync renders the View OUTSIDE
  actionMu.* `runSSEStream` calls `renderFragment(ctx)` on the reconnect branch (sse.go:123) with no
  actionMu held; renderFragment runs `viewFn` → reads `StateTab.Read`/`Signal` `s.val` (unsynchronized)
  while a concurrent action OR a `broadcastRender`→`go c.SyncNow()` writes `s.val`. Every OTHER render
  path holds actionMu (SyncNow, streamTick, action, dispose) — the resync is the one hole. Triggered by
  ordinary reconnects (sleep/deploy/network blip) overlapping an action/broadcast → real `-race` +
  torn reads. Same root cause as [[T3-ARCH-1]] (State/Signal access not serialized off-path). Fix: hold
  ctx.actionMu across the `everConnected.Swap`+resync render, or route resync through SyncNow. Small but
  core-concurrency + touches the recover-path tests → bundle with the T3-ARCH-1 concurrency-contract signoff.

### CONVERGED — high-value DX, queued (meaty, additive)
- `T12-ALL-head` **CONVERGED (UX P1 + WEB P0)** — *no per-page `<title>`/meta/OpenGraph.* Title/desc/lang
  are app-global (config.go:58, render.go:139); a composition can't set its own `<head>` → every page
  shares one title, zero OG/canonical (disqualifying for shareable links/SEO). Fix: optional
  `Head(*CtxR) via.Head{Title,Description,Meta}` (or `Title()`) lifecycle interface discovered by
  reflection like Initializer, threaded into writePageDocument. Additive, fits the optional-interface
  pattern → dedicated TDD tick (new public API).
- `T12-ALL-region` **CONVERGED (WEB P0 top pick + UX P1)** — *whole-View re-renders on every state change*
  (render.go:182-269 re-runs the entire viewFn + morphs the whole `Div(ID(ctx.id))`); no granular/region
  diffing. O(page) Go render + full-page HTML per tick (client morph no-ops the rest). Propose
  `h.Region(id, fn)` keyed to specific slots so flushDirty re-renders only dirty regions; also unlocks
  layout-persists + streamed/loading regions + a live_redirect foundation. Backward-compatible. Large
  architectural item → roadmap/design call.

### VALIDATED — queued (correctness/perf)
- `T12-ARCH-oninit-capacity` **VALIDATED** — `OnInit` runs BEFORE the `tryRegisterCtx` capacity gate
  (render.go:42-65) → over-capacity (503-bound) requests still execute user init work. Fix: move the
  capacity gate before OnInit. → TDD (small lifecycle-ordering change).
- `T12-ARCH-desc-clone` **VALIDATED** — buildDescriptor's shallow clone shares slot slices across mounts;
  invariant is comment-only (composition.go:210). Latent (nothing appends post-clone today). Fix: make
  per-mount fields a separate struct so cmpDescriptor is immutable-after-build (removes the clone). → queue.
- `T12-ARCH-frag-err` **NOTED (defensive)** — renderFragment swallows the h.Render error (render.go:267);
  currently unreachable (strings.Builder never errors) so not a live bug, but inconsistent with the page
  path that logs it. Add a check+log+return"" for consistency. Low priority.
- `T12-ARCH-beginrender-alloc` **NOTED** — beginRender allocates a fresh inflightReads map every render
  (ctx.go:405); on the broadcast hot path that's per-render alloc. Use `clear()` reuse. Perf, queue.

### NOTED / roadmap
- `T12-UX-onDispose-err` — OnDispose is sigVoid; final-persist failures have nowhere to report (showcase
  discards bumpPresence err). Widen to sigErrReturn + recoverLog, or document "log your own".
- `T12-UX-render-500` — a View panic writes via's hardcoded text 500 (render.go:83), bypassing a user
  error page; allow an app-level error-view hook.
- `T12-WEB-patch-target` — Patch is id-morph only; add selector + append/prepend/inner/remove modes
  (datastar already supports them; only the Go surface is missing). Roadmap.
- `T12-WEB-livenav` — client nav is full-page; no live_redirect/SPA swap over the existing SSE stream. Roadmap.
- `T12-ALL-good` — do-not-touch: symmetric panic recovery at EVERY seam (page/fragment/OnInit/OnConnect/
  OnDispose/action) with excellent why-comments, last-good-frame preservation in flushDirty, `*CtxR`
  making writes-in-View a COMPILE error, reflection-once-at-Mount + zero hot-path reflect, holdNotify
  single-frame coalescing, OnInit/OnConnect bot-safe split.

## Tick 11 — RouteFrom + Plugins panel (area 9)

### APPLIED tick 11 (via tdd-rygba; all modules `-race` green)
- `T10-WEB-route` **APPLIED** — `via.RouteFrom(r) string` returns the resolved page route on all three
  entry points (planted via `requestWithRoute` before applyMiddleware in page/action/SSE; mirrors the
  RequestID context pattern). A group guard on an action now sees `/grp/pg`, not `/_action/{id}`.
- `T11-ARCH-pico-cache` **APPLIED** — picocss asset serving now sets `Vary: Accept-Encoding` + a
  representation-specific ETag (gzip variant suffixed `-gz`), so a shared cache can't hand a gzipped
  body to a no-gzip client and a cross-encoding If-None-Match can't 304 the wrong representation
  (RFC 7232/9110). Extracted `writeCSSAsset` helper (deduped both branches).

### CONVERGED — headline, FLAG TO USER (P0, ARCH+WEB top pick)
- `T11-ALL-1` **VALIDATED, FLAG** — *plugins are incompatible with the framework's own strict CSP.*
  `mw.CSP()` emits `script-src 'self' 'nonce-…'`, but every plugin init script — head includes
  (echarts plugin.go:69, maplibre plugin.go:114-115, picocss pico.go:339) AND per-instance `Mount()`
  inline scripts (chart.go:251, map.go:282) — is injected with NO nonce, so they're all blocked →
  charts/maps never initialize, silently. The PUSH path correctly nonces (sse.go:282); the render-time
  path doesn't. maplibre docs even advertise `WithCSPBuild()` → actively misleading. Fix: thread
  `documentCSPNonce` into the page render so head-include + Mount `h.Script` nodes get `nonce=` (Mount
  has no *Ctx today — needs an API), or self-host plugin JS like picocss already does + document the
  CDN `script-src` host addition. Touches render pipeline + plugin Mount signature; browser-verify
  ideal → signoff.

### VALIDATED — queued (security/correctness)
- `T11-UXWEB-doubleinit` **VALIDATED** — echarts/maplibre `initJS` unconditionally `echarts.init`/
  `new maplibregl.Map` and overwrite `window.__viaX[seq]` (chart.go:347, map.go:328) — no
  dispose-before-reinit guard. `data-ignore-morph` protects morph teardown but NOT a container
  re-rendered fresh (conditional View, tab swap) → leaked WebGL context + ResizeObserver. Fix: guard
  `if(existing){existing.dispose()/remove()}` atop initJS. JS behavior → browser-verify ideal. → queue.
- `T11-ARCH-signal-collision` **VALIDATED** — `RegisterAppSignal` keys are an unnamespaced global map;
  routes collide-check (claimRoute) but app signals DON'T → two plugins (or plugin+user) silently
  clobber (picocss owns `_picoTheme`). Fix: panic on duplicate app-signal key (convention-aligned,
  like the wire-key collision panic). → TDD (verify no existing collision first).
- `T11-ARCH-sri` **VALIDATED** — echarts/maplibre load CDN JS pinned by version only, no SRI integrity →
  supply-chain risk. Add `integrity`+`crossorigin` (need correct per-version hash) OR adopt picocss's
  fetch-and-revendor-at-boot model. → flag (hash correctness needs the real asset).

### ROADMAP / docs
- `T11-UX-lifecycle` — no client-lib lifecycle contract (init/re-init-after-reconnect/teardown); each
  plugin hand-rolls `window.__viaX[seq]`+ignore-morph+`?.`-noop. Propose a via-owned keyed-registry +
  idempotent-init + reconnect re-arm primitive. Roadmap (overlaps doubleinit + the SSE re-arm T8-ALL-1).
- `T11-UX-authoring-docs` — docs/plugins.md only CONSUMES the 3 plugins; no "authoring a plugin" guide
  (the Mount-inline-script + registry + ignore-morph contract is source-only). → docs.
- `T11-UX-calljs` — runtime updates are stringly-typed `ExecScript(fmt.Sprintf(...))` with manual
  `mustJSON` XSS discipline; propose structured `ctx.CallJS(target, method, args...)`. Roadmap.
- `T11-WEB-asset-helper` — extract picocss's ETag/gzip/304 serving into an exported `app.ServeAsset`
  so 3rd-party plugins (and apps) reuse it instead of copy-pasting. Roadmap.
- `T11-UX-theming` — picocss `_picoDarkMode` doesn't reach echarts/maplibre; document
  `picocss.DarkModeRef()` as the canonical signal + a client-reactive echarts theme bind. → docs.
- `T11-ALL-good` — do-not-touch: `Plugin()` one-method interface + uniform ctor, functional-options
  panic-on-empty, picocss fetch-revendor-at-boot (bounded LimitReader + timeout) + traversal-safe
  map-lookup serving, push-path nonce capture (first-wins), `data-ignore-morph` on WebGL containers,
  maplibre typed LngLat + `__viaMapReady` re-arm, requireBoot mutation guard.

## Tick 10 — Forms fixes + Middleware panel (area 8)

### APPLIED tick 10 (via tdd-rygba; all modules `-race` green)
- `T9-ARCH-coerce` **APPLIED** — `readMultipartSignals` writes raw strings (removed the JSON-coercion);
  decodeScalarInto coerces per target type. `"007"` survives, a Signal[string] keeps `"true"`, and
  Signal[int]/[bool]/[float] still parse. Audit confirmed no consumer relied on the old typed values.
- `T9-bug-multifile` **APPLIED** — `via.Files` plural handle (Len/All/Key); walker `plural` flag;
  bindFiles/clearFiles/bindFileKeys branch on it. `<input multiple>` no longer drops parts past the
  first. Audit added mixed File+Files + zero-part coverage.
- `T10-ARCH-unwrap` **APPLIED** — `statusWriter.Unwrap()` so http.ResponseController reaches the
  underlying writer's Hijacker/ReaderFrom (AccessLog wraps every request; without Unwrap it silently
  disabled hijacking + sendfile). Verified via a hijacker-capable writer through AccessLog.

### CONVERGED — headline, FLAG TO USER (3/3 seats P0; client redirect behavior)
- `T10-ALL-1` **VALIDATED, FLAG** — *`requireAuth` 303 redirect is silently dead on action/SSE.* Group
  MW runs on all three entry points (page GET app.go:225, action action.go:140, SSE sse.go:61). A
  `http.Redirect(303,"/login")` works for the page GET but on an action/SSE *fetch* datastar follows
  it transparently and feeds `/login`'s HTML to the event-stream parser → no navigation, frozen tab.
  The shipped auth example (auth/main.go:207), the showcase, AND docs/routing-sessions-middleware.md:73
  all teach this broken one-liner; it dead-ends every user on session expiry (the moment a guard
  matters most). Framework owns the right primitive (sse.Redirect) but it's unreachable from the MW
  layer. Fix: `mw.RequireRedirect(pred, dest)` + exported `via.RedirectResponse(w,r,url)` branching
  GET→http.Redirect vs datastar-fetch→SSE redirect frame (reuse safeRedirectURL); rewrite both examples
  + the doc. **Not applied**: client cross-transport redirect behavior, browser-verify blocked → signoff.

### VALIDATED — queued (security/architecture)
- `T10-ARCH-placement` **VALIDATED** — group MW is the INNERMOST wrap on action/SSE: it runs only
  AFTER body/multipart parse, ctx lookup, and the session-403 check (action.go:88-124), and on the
  SSE-recover path AFTER `recoverSSE` registers a fresh ctx + runs OnInit (recover.go:65-90). So an
  unauthenticated request drives body parsing + OnInit side effects before the auth guard fires
  (pre-auth work / unauth OnInit). Also re-wraps the groupMW chain per request (alloc; contradicts the
  cached-chain rationale). Fix: register action/SSE per-descriptor under the group prefix so they flow
  the standard mux+applyMiddleware path (group MW becomes a true outer guard + cached). Routing
  refactor, security-adjacent → careful tick.
- `T10-WEB-route` **VALIDATED** — in the action/SSE MW chain, `r.URL.Path` is `/_action/{id}` or
  `/_sse`, not the guarded page — path-based guards/per-route CORS see the wrong path. Expose
  `via.RouteFrom(r)` (plant resolved route on req ctx before applyMiddleware; mirrors RequestIDFrom). → TDD.

### ROADMAP / docs / noted
- `T10-WEB-breadth` — mw/ has no CORS/rate-limit/gzip/timeout; the auth example has NO rate-limit on its
  bcrypt login → brute-force. Add `mw.CORS/RateLimit/Gzip/Timeout` (gzip MUST skip text/event-stream +
  preserve Flush). Roadmap.
- `T10-WEB-gzipflush` — document the http.Flusher pass-through contract for response-wrapping MW (SSE
  freezes otherwise); ties to Unwrap.
- `T10-ARCH-csp-group` — no composable per-group CSP (app-level only, last-Set-wins); document ordering
  + verify plugin-injected inline `<style>` carries the CSP nonce.
- `T10-ARCH-xfp` — `RedirectHTTPS` trusts X-Forwarded-Proto unconditionally (Strict variant + docs
  mitigate); consider making the safe variant the default.
- `T10-WEB-recover` — `mw.Recover` writes a fixed bare 500 (wrong transport for action/SSE); add
  `WithRecoverHandler` option.
- `T10-empty-group` — document the `Group("")`-as-auth-scope idiom + the global→group→handler ordering.
- `T10-ALL-good` — do-not-touch: group MW covers ALL three transports (incl. 404), outer-first
  applyMiddleware ordering + Defaults rationale, Use panic-after-Start + cached chain, sanitizeLog
  (CWE-117), randID entropy-panic, action-handler LIFO panic recovery layered under mw.Recover.

## Tick 9 — SSE write-deadline + Forms panel (area 7)

### APPLIED tick 9 (via tdd-rygba; all modules `-race` green)
- `T8-ARCH-writedeadline` **APPLIED** — re-arm the SSE write deadline before EACH write in drainQueue
  (Redirect/PatchElements/PatchSignals/ExecuteScript) and the bootstrap PatchElements, so a peer that
  stalls on a later write doesn't get a budget already burned by earlier ones. IO-boundary change,
  verified by build + existing SSE tests (a deterministic mid-drain-stall unit test is impractical).
- `T9-UX-constraints` **APPLIED** — added `h.Pattern`/`h.MinLength`/`h.MaxLength` (native client-side
  constraint-validation attrs; mirrors existing Min/Max). Closes the asymmetry where Required() was
  first-class but pattern/length were Attr() escape-hatch only.

### HELD (not rushed) — SSE concurrency, precise fixes determined
- `T8-ARCH-race` **VALIDATED, queued** — connected-vs-sweep race confirmed: sweep checks `connected`
  under contextRegistryMu (runtime.go:279) + deletes, but the handshake's `connected.Add(1)` (sse.go:77)
  is AFTER getCtx releases the RLock → a stream opening mid-sweep can be TTL-disposed (freeze + spurious
  disconnect{reason=ttl}). Precise fix: increment `connected` UNDER the registry RLock during stream
  ctx-acquisition (sweep's Lock then serializes & sees connected>0). Touches handshake+recover+dispose;
  no deterministic test for a timing race → focused careful tick, not rushed.
- `T8-ARCH-backpressure` **queued** — cap per-ctx elements/scripts (risk: dropping correctness frames,
  per prompt warning) → careful design tick.

## Tick 9 (part 2) — area: Forms & validation

Panel convened (UX, ARCH, WEB), grounded in form.go/file.go/action.go(multipart)/
encoding.go/config.go + examples/auth+upload + docs/file-uploads.md.

### VALIDATED SECURITY — FLAG TO USER (coupled, changes upload behavior)
- `T9-ARCH-upload` **VALIDATED, FLAG** — two coupled findings:
  - **#2 (live): memory-exhaustion DoS.** `readMultipartSignals` passes `maxBody` as BOTH the
    MaxBytesReader wire cap AND ParseMultipartForm's in-memory limit (action.go:79,99; file.go:205) →
    up to ~32MiB buffered on the heap per concurrent upload, never spilling (config.go:238 comment
    claims a separate cap that doesn't exist). N uploads → N×32MiB heap.
  - **#1 (latent, activated by fixing #2): temp-file leak.** `form.RemoveAll()` is deferred only inside
    runAction (action.go:232); reject paths (tab-not-found 116, session-mismatch 123, action-404 129,
    middleware-deny 140) never clean spilled temp files. Currently unreachable BECAUSE memCap==wireCap
    means files never spill — fixing #2 makes them spill, activating this leak.
  - Coupled fix: lower the mem limit (e.g. `min(maxBody, 8<<20)`) so large uploads spill to disk, AND
    `defer form.RemoveAll()` in handleAction right after parse (covers all exit paths; remove the
    runAction defer). Changes upload memory/spill behavior (perf) → signoff. Testable with t.Setenv TMPDIR.

### CONVERGED — headline DX (3/3 seats P0), queued TDD
- `T9-ALL-1` **CONVERGED** — *no per-field validation-error primitive.* Every form collapses errors into
  one blob + flags ALL inputs aria-invalid at once (a real correctness bug: one bad field marks every
  input — auth.go:42,46,97,101). DecodeForm is best-effort/never-fails and used exactly ONCE in the
  whole codebase (maplibre). Proposed: a reflection-bound `via.Errors` map value-type + `errs.Field(name)`
  helper that emits aria-invalid + aria-describedby + `<small role=alert>`. Unlocks LiveView-style live
  validation (on.Input+Debounce plumbing already exists), gives DecodeForm a purpose, fixes the a11y +
  coarse-flagging bugs. ~40 lines, fits "errors are values". → dedicated TDD tick (meatier new API).
- `T9-WEB-livevalidation` — no on-change validation idiom; document `on.Input(p.Validate, Debounce)` +
  the Errors primitive. → with T9-ALL-1.

### VALIDATED correctness — queued
- `T9-ARCH-coerce` **VALIDATED** — multipart text fields are JSON-coerced (file.go:223-235): `"007"`→7,
  and a string-typed signal whose value is `true` silently stays empty (decodeScalarInto string-case
  doesn't accept bool). Inconsistent with the JSON path. Fix: don't coerce multipart text — leave
  strings, let decodeScalarInto's string case handle numeric/bool targets. → small TDD.
- `T9-ARCH-tag` — DecodeForm keys off `form:` tag while File/Signal use `via:` → two namespaces per
  struct. Unify on `via:` or document loudly.
- `T9-bug-multifile` — `via.File` binds only `form.File[key][0]` → `<input multiple>` silently drops all
  but the first; add a `via.Files` plural handle. → TDD.

### ROADMAP / docs
- `T9-WEB-upload-cliff` — uploads require dropping to a raw `<form>` (no on.Indicator/progress/preview/
  drag-drop, full-page redirect); document the fetch-with-progress recipe + surface 413 inline.
- `T9-UX-413` — oversize upload returns a bare 413 text page, not an in-form message.
- `T9-WEB-formstate` — no dirty/pristine/touched; document as a client Datastar recipe.
- `T9-ALL-good` — do-not-touch: File.Save 0o600+O_TRUNC + untrusted-Filename/ContentType godoc (no
  path-traversal sink), per-content-type MaxBytesReader→413, MultipartReader streaming escape hatch,
  composite decode zero-before-unmarshal.

## Tick 8 — audit cleared + SSE/streaming panel (area 6)

### Tick-7 deferred audit: CLEARED
Blue+Audit on the 3 session fixes ran clean — no production bugs. All cookie sites confirmed routing
through `a.cookieName()` (incl. Rotate); touchSession nil-safe + atomic; metric naming OK. Added one
test: `WithSessionCookieName("")` panic. All modules `-race` green.

### APPLIED tick 8 (via tdd-rygba; sse.go; all modules `-race` green)
- `T8-WEB-buffer` **APPLIED** — set `X-Accel-Buffering: no` on the SSE response so nginx/proxies don't
  buffer the stream (frames/heartbeat would otherwise be held until the buffer fills). datastar's
  NewSSE sets Cache-Control/Content-Type/Connection but not this. (WEB seat over-claimed "no headers" —
  Cache-Control IS set by the SDK; X-Accel-Buffering was the real gap.) Test asserts the header.

### CONVERGED — headline, FLAG TO USER (client behavior + browser-verify blocked)
- `T8-ALL-1` **VALIDATED, FLAG** — *graceful-deploy / SIGTERM freezes every tab* (3/3 seats P0).
  Datastar resolves on a CLEAN stream close and never re-arms; the page bootstraps `@get('/_sse')`
  once (render.go:129). So a rolling deploy drains gracefully → every tab silently stops streaming
  until manual reload, and recover.go never fires (browser never reconnects). Matches
  [[project_datastar_no_retry_on_clean_close]]. Candidate fix: inject a `datastar-fetch`
  finished/retries-failed re-arm listener in the page head (sibling of the beforeunload beacon),
  re-firing `@get('/_sse')` with backoff+jitter so recoverSSE gets its chance. **Not applied**: it's
  client-side reconnect behavior with a real reload-loop risk if mis-gated, and browser-verify is
  blocked (port). Needs signoff + in-browser verification before shipping.

### VALIDATED — queued TDD (concurrency / correctness)
- `T8-ARCH-race` **VALIDATED** — connected-counter vs TTL-sweep check-then-act race: sweep reads
  `connected` under contextRegistryMu (runtime.go:279) but the handshake does `connected.Add(1)`
  WITHOUT that lock (sse.go:77) → a just-opening stream's ctx can be TTL-disposed mid-handshake →
  freeze + a spurious `disconnect{reason=ttl}` (which also explains the only way the reason catalogue
  could drift). Fix: re-check connected under the registry lock at dispose, or register stream-intent
  under the lock. → TDD.
- `T8-ARCH-backpressure` **VALIDATED** — per-ctx `elements`/`scripts` queue grows unbounded
  (push.go:101, runtime.go:244) for a slow/half-open client; only signals are capped (256). Fix: cap +
  coalesce + a `via.sse.backpressure` counter. Ties scale items 9-10. → TDD (bigger).
- `T8-ARCH-writedeadline`+`T8-ARCH-livenesstimeout` **CONVERGED (ARCH highest-conviction)** —
  `sseWriteTimeout<=0` makes the per-drain deadline a no-op, so a half-open peer's heartbeat write can
  block forever → `connected` never drops → ctx immortal (TTL-immune) + unbounded backpressure. Also
  the deadline is set once per drain, not per write. Fix: enforce a non-zero `sseWriteTimeout` default
  + re-arm the deadline before each write in drainQueue/bootstrap. → TDD (changes a default — note migration).

### NOTED / docs / roadmap
- `T8-WEB-heartbeat` — heartbeat is a data `PatchSignals("{}")`, not a cheap comment frame
  ([[feedback_sse_heartbeat_design]] agrees); a comment is idiomatic but interacts with brotli (the
  `: ready` marker is suppressed under compression). Resolve carefully. → queued.
- `T8-UX-resync` — reconnect resync replaces the whole view → focus/scroll/flicker loss; doc the
  caveat + consider WithViewTransition on the resync patch.
- `T8-UX-indicator`/`T8-WEB-alerting` — no connecting/offline indicator UX; document the
  `datastar-fetch` event contract (started/retrying/retries-failed) + an optional
  `WithConnectionIndicator()`; add SSE stream-health alerting guidance to production.md.
- `T8-WEB-reloadstorm` — `mode=reload` recovery (recover.go:222) has no jitter → thundering-herd
  reload after a deploy/at-capacity; add setTimeout jitter.
- `T8-WEB-http2`/`T8-WEB-proxy-docs` — document HTTP/2 (dodge the 6-conn/origin SSE cap) + nginx
  `proxy_buffering off; proxy_read_timeout > heartbeat` in production.md.
- `T8-disconnect-reason` **RESOLVED/NOT-A-BUG** — via.sse.disconnect DOES emit `reason` (sse.go:72);
  catalogue correct by the ttl-never-reaches-disconnect invariant. Stale memory note updated. Don't re-file #55.
- `T8-ALL-good` — do-not-touch: recover.go stale-id re-bootstrap (fresh server-minted tab, preserves
  CSRF), per-write deadlines, force-drain on reconnect, resync ships view-not-signals, shutdown
  ordering, signalDispose idempotent chokepoint, hoisted heartbeatPayload + pooled signals map.

## Tick 7 — applied session wins (via tdd-rygba; all modules `-race` green)

- `T6-ALL-2` **APPLIED** — 403 observability: action.go + sse.go(handshake) mismatch branches now
  emit `via.session.mismatch` metric + a `logErr` naming the likely cause (two via apps clobbering
  via_session). No behavior change. Test forges the mismatch via a cookie-less action on a
  session-bound tab.
- `T6-UX-cookie` **APPLIED (opt-in only)** — `WithSessionCookieName(name)` + `a.cookieName()` threaded
  through getOrCreateSession/sessionCookie/sessionFromRequest. Co-located localhost apps can now pick
  distinct cookie names to avoid the clobber. **Deliberately did NOT change the default to
  `__Host-via_session`**: that would silently invalidate every existing prod session on deploy (cookie
  rename = forced logout) — the `__Host-` default belongs in the signoff bucket (T6-ARCH-fixation).
- `T6-UX-ttl` **APPLIED** — `ctx.touchSession()` bumps the bound session's lastAccess on the SSE
  keepalive tick, so a live-but-idle streaming tab's session isn't reaped underneath it (next action
  would otherwise 403). Helper unit-tested (advances + nil-safe); keepalive wiring verified by build
  (the 25s keepalive floor makes an end-to-end timing test impractical).

### ⚠ Tick 7 audit + SSE-panel NOT RUN — session limit hit (resets 2:40am)
The Blue+Audit on the 3 session fixes AND the SSE/streaming panel (queue area 6) both failed to run:
all 4 subagents returned empty on a session-budget limit. The code changes are green but UNAUDITED.
Next tick MUST: (1) run the deferred audit on the 3 session fixes (esp. verify Session.Rotate's
cookie path uses a.cookieName() — a write/read name mismatch would silently break sessions), then
(2) convene the SSE/streaming panel. Do these BEFORE advancing to a new queue area.

## Tick 6 (part 2) — area: Sessions, via_tab, CSRF model

Panel convened (UX, ARCH, WEB), grounded in sess.go/crypto.go/util.go/action.go/sse.go/
server.go/config.go/recover.go + examples/auth + docs. (via_tab=CSRF token grounding honored.)

### CONVERGED — headline (3/3 seats) — FLAG: security-gate behavior, signoff
- `T6-ALL-1` **VALIDATED, FLAG TO USER** — *session-mismatch 403 is a dead-end.* The unknown-tab SSE
  branch self-heals via `recoverSSE` (sse.go:44) but the known-tab-wrong-session branch just 403s
  (sse.go:47, action.go:119) → datastar exhausts retries → frozen tab. This IS the documented
  localhost cookie-clobber freeze ([[project_session_403_freeze]] — candidate fix matches: re-bootstrap
  on mismatch too). Seats argue CSRF is preserved (via_tab already validated; fresh tab server-minted).
  Touches the security gate → maintainer signoff before implementing. Recommended: route mismatch
  through recovery + re-bind ctx.session to the request's session.

### CONVERGED — safe / additive (queued TDD)
- `T6-ALL-2` **CONVERGED (safe, no behavior change)** — *403 is undebuggable*: bare status, no log,
  no metric (vs the rich panic path action.go:215). Add `logErr` + `via.session.mismatch` counter
  naming the likely cause ("multiple via apps on same host:port?"). → TDD, smallest next.
- `T6-UX-cookie`+`T6-ARCH` **CONVERGED** — *no `WithCookieName` escape hatch*; two localhost apps share
  `via_session`/Path=/ (port isn't cookie scope) → guaranteed clobber. Add `WithSessionCookieName`
  (functional-option, mirrors WithInsecureCookies); default `__Host-via_session` under Secure
  (free hardening). Cookie attrs CAN'T separate by port — name is the real fix. → TDD.
- `T6-UX-ttl`+`T6-ARCH-gc` **VALIDATED** — *a live SSE tab's session can expire underneath it*:
  `lastAccess` only bumps on getOrCreateSession (request path), not the SSE heartbeat; connected ctx
  is pinned but its session isn't → 31-min idle dashboard → next click 403. Fix: touch session
  lastAccess on the SSE heartbeat (mirror ctx liveness pin). → TDD.

### FLAG TO USER (security) — re-confirmed + new
- `T5-ARCH-sid` **RE-CONFIRMED (ARCH highest-conviction)** — raw 256-bit session id (the bearer secret
  + CSRF root) written verbatim into backplane snapshot keys + change hints (statesess.go:103,135) →
  readable by every pod / persisted in logs/Redis/Kafka. Fix: HMAC/sha256 the sid for backplane keys
  (crypto.go has primitives; pods match by hash); no public API change. Confidentiality break in prod.
- `T6-ARCH-fixation` **NEW FLAG** — cross-pod session adoption (sess.go:177-208) accepts any well-formed
  client-presented sid (shape-checked only) → session-fixation primitive; `Rotate` exists but is opt-in
  and nothing auto-calls it on auth boundary. With no `__Host-` prefix the injection vector is wider.
  Recommend: `__Host-` default + document adoption=fixation-exposed + rotate-on-auth hook. → signoff.

### ROADMAP (state for product use)
- `T6-WEB-store` **ROADMAP (WEB P0, highest-conviction)** — no `SessionStore` interface → every restart/
  deploy = global logout; also blocks revocation / "log out everywhere" / "remember me" / multi-device.
  Add `SessionStore` interface + `WithSessionStore` (default in-memory); ~4 call sites in sess.go.
  Precedent: Backplane/KeyStore. Large but keystone.
- `T6-WEB-handlefunc` **NOTED** — plain `Group.HandleFunc` POSTs are outside the via_tab/CSRF model;
  document + consider `mw.CSRF()` helper.
- `T6-WEB-authmw` **NOTED** — `requireAuth` 303 redirect breaks for action/SSE requests (datastar can't
  follow); provide `mw.RequireAuth` emitting a datastar location redirect for action/SSE.
- `T6-ALL-good` — do-not-touch: via_tab-as-CSRF (256-bit, unknown→404, server-minted on recover, never
  adopts client id as identity), fail-closed RNG panic (util.go:14), Secure-default + WithInsecureCookies
  conflict-panic, typed `sess.Get/Put[T]` API, cross-pod adoption design, withSession mints only on matched routes.

## Tick 5 (part 2) — area: State model (StateTab/Sess/App + event-log backplane)

Panel convened (UX, ARCH, WEB), grounded in state.go/statesess.go/stateapp.go/
stateappevents*.go/broadcast.go/appval.go + docs.

### VALIDATED — queued TDD (clean pure-correctness)
- `T5-ARCH-2` **VALIDATED** — `applyChange` broadcasts unconditionally (appval.go:259) even when
  the monotone gate didn't change L1, while `reconcileKey` (appval.go:173) correctly gates on
  `changed` ("a steady-state sweep is a silent no-op, not a render storm"). Tailer path violates the
  same contract → stale hints wake every tab. Fix: hoist `changed`, gate the broadcast. → tick 6.

### CONVERGED / strong (queued; several docs + roadmap)
- `T5-ARCH-1`+`T5-WEB-4` **CONVERGED (scale P1)** — render storm: `broadcastRender` fires
  `go c.SyncNow()` per subscribed tab per write (broadcast.go:74), uncoalesced. Fix: route cross-ctx
  wakes through dirty-bit + `queue.notify()` so each tab renders once per drain. Ties scale items 9-10.
- `T5-WEB-3`+`T5-ARCH-silent` **CONVERGED** — silent writes don't propagate cross-pod: SyncOff
  suppresses the changes-feed hint (stateapp.go:141, statesess.go:134) → peers never converge on
  default config. Fix: silent suppresses render fan-out, NOT the value-less durability hint. → TDD.
- `T5-UX-1`+`T5-WEB-5` **CONVERGED (docs)** — no single "Choosing a scope" decision at the call
  site (5×2 matrix). Add a decision section to reactive-state.md + back-link from godocs.
- `T5-UX-3`+`T5-WEB-2` **CONVERGED (docs)** — StateAppEvents read-your-write is eventual even for
  the writer's OWN action (stateappevents.go:121-130); surface plainly in distributed-state.md.

### VALIDATED security — FLAG TO USER
- `T5-ARCH-sid` **FLAG** — StateSess change hint marshals the FULL raw session id onto the shared
  `via.changes` backplane (statesess.go:133, appval.go:208-212) → every pod/tenant on a shared bus
  sees every live session id in the clear (enumeration/privacy; sid IS the CSRF token per
  [[feedback_csrf_threat_model]], ties [[project_session_403_freeze]]). Fix: HMAC the sid in the
  hint. → maintainer signoff (security design).

### NOTED / ROADMAP (state)
- `T5-ARCH-tabatomic` — `StateTab.Update` godoc says "atomically" w/ no sync; doc-only, bundle with T3-ARCH-1.
- `T5-WEB-1` ROADMAP — no presence primitive; proposed `via/presence` on StateAppEvents + disconnect hook.
- `T5-WEB-opt` ROADMAP — no client optimistic-update/rollback; opt-in optimistic-signal reconciliation.
- `T5-WEB-ctx` — Append/Update hardcode context.Background() (stateappevents.go:156); thread req ctx.
- `T5-ARCH-snap` — snapshot `Compacted` predicts floor via `prevSnapOffset>=2`; derive from realized compaction.
- `T5-ALL-good` — do-not-touch: single-writer projector, Append-never-conflicts churn fork, monotone
  rev gate (hints advisory/Store is truth), fail-closed unknown-sid, snapshot-first/compact-second.

### DEBATING / NOTED
- `T3-ARCH-3` **NOTED** — shape_*.go wrapper matrix (5 shapes × 4 scopes) duplicates ~20 identical
  `Op` bodies; largely inherent to Go (no HKT). Optional: `newOps[T](update)` helper to one-line each.
- `T3-ARCH-4` **DEBATING** — `BoolOps.True()/False()` are setters that read as predicates; rename
  `SetTrue()/SetFalse()` for verb consistency. Breaking — weigh churn.
- `T3-ARCH-5` **DEBATING** — `Read(_ readCtx)` ignores its arg (signal.go:35); speculative param.
  Resolution couples to T3-ARCH-1 (if Read must lock for cross-goroutine safety, the param earns
  its keep; else drop it). Decide alongside the race fix.
- `T3-WEB-3` **DEBATING** — `Bind` has no debounce/number/lazy modifiers; add `Bind(opts...)`.
- `T3-WEB-4`/`T3-UX-7` **DEFERRED** — keyed client list reactivity (`data-for` over SignalSlice);
  architectural, overlaps T1-UX-4 EachKeyed. Revisit in a dedicated reactivity-scaling tick.
- `T3-ALL-good` — do-not-touch: `Op(ctx)` chains, `Update(fn)(T,error)` single mutation primitive,
  unexported-fields + marker-interface reflection binding, allocation-aware `encodeScalar`
  (strconv.Append, float32-widening), honest silent-truncation doc.

- `T2-ALL-good` — do-not-touch: `on.Click(c.Inc)` reflection binding + `notMethodPanic`
  nil/closure/top-level split (best DX in the API), boot-time `verifyMethodNameTrampoline`
  canary, type-safe `SetSignal[T]`, per-tab action serialization correctness, bareAttr
  interning.

---
