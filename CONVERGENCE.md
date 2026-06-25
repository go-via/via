# via/v2 — convergence v2 (showcase + feature-complete plan)

Ratified by the robust expert council. Build order is fixed:
**routing → compose(If/When/Each/Embed) → sugar(Local/On*/Counter/Append) → chat showcase.**

I have everything I need from the verdicts and the codebase evidence. Writing the SHOWCASE section now.

---

# SHOWCASE — The Flagship: Live Multi-User Chat with Presence

## The app

A single-room **live chat** with a "N online" presence header, a keyed message log that fans out to every connected tab, and a composer that clears on send. One Go file, ~50 lines of app code, zero JavaScript, zero `&`, zero identifier strings, zero closures at call sites.

## Why chat is the right flagship

Chat is the *minimal* app that lights up every via strength at once and reaches nothing it can't honestly deliver:

- **Live multi-user fan-out** — the headline. `via/topic` already turns one `Publish` into a frame on every island's SSE. Chat is its canonical consumer; a message typed in one tab appears in all of them.
- **Interactive write-back** — sending a message is the exact gesture that exercises the live-island action path (the boundary the council just resolved: POST → registry → island `pulse` → re-render → SSE push → 204). Chat puts that mechanism on the screen as its spine, not a footnote.
- **Lifecycle presence** — "3 online" is a second `State` driven by `OnConnect`/`OnDispose`, proving the connection lifecycle visibly.
- **Keyed lists** — a growing message log forces `Each` with stable identity (the structural-key contract), the one composition feature chat genuinely needs.

Kanban needs drag/drop (client JS — breaks zero-JS), poll is a counter with bars (undersells composition), todo is single-user (no fan-out). Chat sits precisely on the achievable frontier and *looks alive in a screen recording*. **Choose chat.**

## Tasteful polish — classless theme, no plugin system

Do **not** build a plugin/class system. Ship one ~30-line embedded stylesheet via a `via.WithTheme()` functional option that injects a `<style nonce>` into `<head>` and styles **bare semantic tags** (`body`, `h1`, `ul`, `li`, `form`, `input`, `button`): system-font stack, a centered max-width column, hairline borders, and a sticky bottom composer. Because it's classless, the *View stays class-free* — no `h.Class("…")` soup polluting the ideal code. Opting out is just omitting `WithTheme()`. This requires the one real CSP edit the council flagged: add `style-src 'self' 'nonce-…'` to `cspPolicy` (`csp.go` emits none today) and surface the style nonce. Polish becomes "almost effortless": write semantic HTML, the theme makes it look intentional.

## The ideal user-facing code — the DX north star

This is the bar. It must read like the `counter` example — *that* is the achievement.

```go
package main

import (
	"net/http"

	"github.com/go-via/via/v2"
	"github.com/go-via/via/v2/h"
	"github.com/go-via/via/v2/topic"
)

type Message struct {
	ID   int64
	Who  string
	Text string
}

// Room is the shared, process-wide hub. App-land, not framework.
type Room struct {
	bus      *topic.Topic[Message]
	presence *topic.Topic[int] // broadcasts the current head-count
}

// Chat is one connected tab's island instance.
type Chat struct {
	room *Room

	Who    via.Local[string]    // client-resident; never round-trips to the server
	Draft  via.Signal[string]   // two-way bound input, cleared on send
	Log    via.State[[]Message] // server-authoritative, pushed over SSE
	Online via.State[int]       // presence count, pushed over SSE
}

// --- lifecycle ---

func (c *Chat) OnConnect(ctx *via.Ctx) error {
	msgs := c.room.bus.Subscribe()
	ctx.OnDispose(msgs.Stop)
	via.Subscribe(ctx, msgs.C(), c.onMessage)

	heads := c.room.presence.Subscribe()
	ctx.OnDispose(heads.Stop)
	via.Subscribe(ctx, heads.C(), c.onPresence)

	c.room.join()             // publishes the new +1 count to everyone
	ctx.OnDispose(c.room.part) // publishes the -1 on disconnect
	return nil
}

func (c *Chat) onMessage(ctx *via.Ctx, m Message) { c.Log.Append(ctx, m) }
func (c *Chat) onPresence(ctx *via.Ctx, n int)    { c.Online.Set(ctx, n) }

// --- the live-island action: mutates THIS connection's island ---

func (c *Chat) Send(ctx *via.Ctx) {
	c.room.bus.Publish(Message{ID: nextID(), Who: c.Who.Get(), Text: c.Draft.Get()})
	c.Draft.Set(ctx, "") // clear the composer; the message returns via fan-out
}

// --- view (pure, ctx-free) ---

func (c *Chat) row(m Message) h.H {
	return h.Li(h.B(h.Str(m.Who+": ")), h.Str(m.Text))
}

func (c *Chat) View() h.H {
	return h.Div(
		h.H1(h.Str("Room — "), c.Online.Display(), h.Str(" online")),
		h.Ul(via.Each(c.Log, c.idOf, c.row)), // keyed list; c.row is a method value
		h.Form(
			h.Label(h.Str("You "), h.Input(c.Who.Bind())),
			h.Input(c.Draft.Bind(), h.RawAttr("placeholder", "message")),
			h.Button(via.OnClick(c.Send), h.Str("send")),
		),
	)
}

func (c *Chat) idOf(m Message) int64 { return m.ID } // stable key for morph-by-id

func main() {
	room := &Room{bus: topic.New[Message](), presence: topic.New[int]()}
	http.Handle("/", via.Register(Chat{room: room}, via.WithTheme()))
	http.ListenAndServe(":8080", nil)
}
```

That is the whole app. No template strings, no event-name literals, no `&`, no closures, no `any`. `Send`, `onMessage`, `onPresence`, `row`, `idOf` are all named method values — the AST lint stays green. `View()` is pure and ctx-free; the live action threads through `Ctx`. `Who` is a `Local` (the `_`-prefixed client signal that never reaches the server); `Draft` is a server-bound `Signal`; `Log`/`Online` are server-authoritative `State`. A Go dev who wrote the counter already knows how to read every line.

## What makes it delightful

- **It's just structs and methods.** Presence, fan-out, and write-back are expressed as ordinary Go: a `Room` holds two topics, `OnConnect` subscribes, an action publishes. No framework vocabulary leaks into the app beyond `via.Local/Signal/State`, `via.Each`, `via.OnClick`, `via.Subscribe`.
- **The dangerous parts are invisible.** The tab-id handshake, the per-connection registry, the `pulse`-channel serialization, the 204-then-push protocol, morph-by-key — all framework-internal. The user never sees a tab id or a wire name.
- **The compiler is the guardrail.** `Local[T]` has no `Set(ctx,…)` server doorway; `View()` can't read `State` off a dead island; `Each` *requires* a key function. Misuse is a build error, not a runtime surprise.

## The DX north star, distilled

> A live, multi-user, presence-aware application that reads like a static one — pure `View`s, named methods, and three signal kinds (`Local`, `Signal`, `State`) whose *types* tell you exactly where each value lives. The framework earns its keep by making the hard distributed parts disappear, not by adding vocabulary.

**Sequence to make this code compile and run** (per the ratified verdicts): (1) live-island action routing — lift `pulse` out of `runLiveStream`, per-tab registry, `X-Via-Tab` header on the POST, dispatch-onto-`pulse`, return 204; (2) `via.Local[T]`; (3) `via.Each` keyed + `h.Ul/Li/B` tags; (4) `State[T].Append`; (5) `via.WithTheme()` + the `style-src` CSP line; (6) presence wiring in the example. Item 1 is the keystone — once a POST reaches the connection's island through the `pulse` channel, the framework is genuinely interactive and the code above is real.

Files of record: `/home/jgonc/Personal/repos/via-v2/via.go` (POST `inst := root` boundary, SSE + GET handlers), `/home/jgonc/Personal/repos/via-v2/live.go` (`pulse` — the injection point to lift), `/home/jgonc/Personal/repos/via-v2/state.go`, `/home/jgonc/Personal/repos/via-v2/signals.go`, `/home/jgonc/Personal/repos/via-v2/topic/topic.go`, `/home/jgonc/Personal/repos/via-v2/csp.go` (`style-src` edit), `/home/jgonc/Personal/repos/via-v2/example/feed/main.go` (closest existing shape).
---

I now have everything load-bearing confirmed. Key facts that decide the contested points:

1. **Lint whitelist exists** (`viaCallNames`) and already reserves `OnSubmit/OnInput/OnChange/Embed` — adding `If/When/Each` builder positions requires extending it, OR keeping those eager/structural (no FuncLit arg). `When`/`Each`/`Embed` take `func` args → they WILL trip the FuncLit check unless whitelisted. Note: the lint only flags FuncLit args to *via-prefixed* calls in `viaCallNames`. `If` takes an eager `h.H` (no closure). `When`/`Each` take builder closures → must be whitelisted but a `func(T) h.H` row-builder passed as a *named method value* passes; a literal trips. Decision: whitelist them.

2. **Tab-id: header vs signal** — `shapeMatches` requires `len(order)==len(in)`. A `_viatab` in the body but not in `order` → 410 on every live POST. Two verdicts split (header vs `Local`-excluded-from-shapeMatches). The header approach is cleaner and conflict-free; but on a live island, **`shapeMatches` should not gate at all** (sequencing verdict moved the guard inside the island re-render). I'll resolve: live islands skip `shapeMatches`, tab id rides a header. Belt-and-suspenders.

3. **`pulse` must be lifted** — unanimous across verdicts.

4. **CSP** has no `style-src`; `WithTheme` is out of scope for this decisions doc (not in my charge: live-routing, Embed, If/When/Each, Local, ergonomics sugar).

Writing the RESOLVED API DECISIONS section now.

---

# RESOLVED API DECISIONS

Chair's note: every signature below is buildable on the current tree (`via.go`, `live.go`, `state.go`, `signals.go`, `h/h.go`) with no rewrite. Sequence is fixed: **(1) live-island routing → (2) If/When/Each + Embed → (3) Local + ergonomics sugar → chat showcase.** Build each with `tdd-rygba`.

---

## 1. Live-island action routing (the `_viatab` handshake)

The blocker: a POST runs `inst := root` (via.go:346), a fresh copy — it cannot mutate *this connection's* island `State`. Resolution: mint a per-connection tab id, register the connection's **pulse channel**, and route the POST's action fn onto that channel so it runs on the island's existing single goroutine.

```go
// live.go — lift pulse out of runLiveStream onto a registered per-conn record.
type liveConn struct {
    inst  viewer                 // the island instance (carries State[T])
    pulse chan func(*Ctx)        // the island's serialization channel
    ctx   *Ctx                   // island Ctx from OnConnect (ticks/subs/disposers)
    done  <-chan struct{}        // reqCtx.Done()
}

// registry is a per-app (per-Register) map; a local in Register, never global.
type registry struct {
    mu sync.Mutex
    m  map[string]*liveConn
}
func (r *registry) put(id string, c *liveConn)
func (r *registry) get(id string) (*liveConn, bool)
func (r *registry) del(id string)

// runLiveStream now RECEIVES the channel (no longer creates it) and ALWAYS loops
// — drop the no-ticks/no-subs early-return so an interactive-only island still
// accepts dispatched actions.
func runLiveStream(reqCtx context.Context, island *Ctx, inst viewer,
    pulse chan func(*Ctx), push func())

// Dispatch routes one action fn onto the island goroutine; select-guarded so a
// POST racing a just-closed tab never blocks on the unbuffered channel.
func (c *liveConn) Dispatch(fn func(*Ctx)) bool // false → caller writes 410
```

**Tab id rides a request header, not a signal.** The SSE handler mints `id := genTabID()` (reuse the `crypto/rand` base64 minting in `csp.go`), pushes one signals frame before the loop, and registers the conn:

```go
func genTabID() string                          // 128-bit URL-safe base64
func writeSignalsFrame(w io.Writer, raw []byte)  // event: datastar-patch-signals
// OnClick/On* emit: data-on:click="@post('/_via/a/N',{headers:{'X-Via-Tab':$_viatab}})"
```

POST handler, live branch: read `X-Via-Tab`, `reg.get(id)`; miss → 410. On hit, **skip `shapeMatches`** (the island re-render is the authority, not the request echo) and dispatch a fn that **re-binds against the island instance**, never the throwaway:

```go
ok := lc.Dispatch(func(ic *Ctx) {
    bind, _ := renderRoot(lc.inst, in, true) // rebind on the LIVE inst
    if n >= 0 && n < len(bind.actions) { bind.actions[n]() }
})
// ok → 204 (push() over the SSE ships the patch); !ok → 410.
```

**Why a header, not a `_viatab` signal:** `shapeMatches` requires `len(order)==len(in)` and every `in` key ∈ `order` (via.go:61–70). A `_viatab` signal sits in the body, never in `order` → every live POST 410s. A header sidesteps the body shape-guard entirely at one fixed template-string cost, identical to a signal-filter regex, and removes the conflict outright.

**Guarantees intact:** no reflection (registry is a plain map; liveness still by interface assertion), no `&`, no user strings (`X-Via-Tab`/tab id are via-minted), no closures at user call sites (registry + dispatch fn are library-internal), View stays pure/ctx-free. CSRF: keep `originAllowed` as the floor; the unguessable tab id is the real second factor (a header is forgeable same-origin, the id is not).

**Mandatory hazards (close all three):** (a) `Dispatch` send is `select { case pulse<-fn: case <-done: }` — never bare; (b) `reg.del(id)` is `defer`'d in the SSE handler alongside disposers, so a panic can't leak the entry; (c) `State` resets on reconnect — correct semantics, document it for the chat showcase.

---

## 2. Composition & lists — `Embed`, `If`, `When`, `Each`

These are structural primitives. `Embed` already sits in the lint whitelist; `When`/`Each` take builder funcs and **must be added to `viaCallNames` arg-position carve-outs** (confirmed extensible — it's a `map[string]bool`, guarantee_test.go:19), or they trip the FuncLit ban.

```go
// compose.go
func If(cond bool, node h.H) h.H                              // eager; no closure
func When(cond bool, build func() h.H) h.H                    // lazy branch (whitelist)
func Each[T any](items []T, key func(T) string,               // keyed list
                 row func(T) h.H) h.H                          // (whitelist)
func Embed[T any, PT interface{ *T; viewer }](child T) h.H     // nested composition
```

**Action-index soundness under composition.** Because action ids are positional (`ActionSlot`, via.go:108), a branch or list that changes shape between renders renumbers the table. The cursor that fixes this lives in `Ctx` at render time and is keyed per structural path, **not** a single global ordinal:

```go
type pathSeg struct{ tag uint32; ord uint32; localOrd uint32 } // localOrd lives ON the seg
func (c *Ctx) PushPath(tag uint32, ord uint32)
func (c *Ctx) PopPath()
// ActionSlot folds the path stack + the top seg's localOrd++ into the id, so two
// OnClicks at one path, or an action after a sibling Dyn that pushed/popped, never
// collide. foldPath: FNV-1a → base36.
```

`If` is eager (takes a built `h.H`) so it never trips the closure lint. `Each`'s `key func(T) string` returns **app data** (e.g. a message id), not a via-minted string — no user-facing identifier leaks. `EachIndexed` is **rejected for rows that carry actions** (index keys + actions = renumber footgun); if added, document it as action-free only.

**Guarantees intact:** no reflection (`Embed` uses the same `PT interface{ *T; viewer }` sentinel as `Register`), no `&` (child taken by value, auto-addressed), no user strings (keys are app data; path tags are folded hashes), no `any` (sealed `h.H` throughout). View stays pure — the cursor is binder state, not View state.

---

## 3. `Local[T]` — client-only signal

`Local[T]` is `Signal[T]` minus the server doorway: it skips the hydration branch in `bind` (via.go:139) and exposes no `Get`/`Set`. Make it a **subtype** so `Bind()`/`Display()` are inherited and the `_`-prefix lives in one place — not a free naming hack.

```go
// signals.go
type Local[T any] struct{ Signal[T] }     // inherits Bind()/Display()
// bind() override emits a "_"-prefixed slot name (Datastar local-signal
// convention; never sent to the server) and skips SignalInit hydration.
func Show(*Local[bool]) h.Attr            // data-show="$_sN"
func Class(name string, on *Local[bool]) h.Attr
```

The `_` prefix means Datastar never POSTs it — so it is naturally absent from the request `in` and **cannot disturb `shapeMatches`** (it's never declared on a server bind pass; client-only). That property is the whole point: optimistic UI (input mirror, toggles, show/hide) with zero server round-trip, while `State[T]` stays the server truth.

**Guarantees intact:** no server accessor by construction (compiler-enforced — no `Get`/`Set` exported), no `&`, no strings (`_sN` is via-minted), no closures.

---

## 4. Ergonomics sugar — `On*`, numeric ops, `State.Append`

```go
// signals.go — event helpers. OnSubmit/OnInput/OnChange already lint-reserved.
func OnSubmit(fn func(*Ctx)) h.Attr   // data-on:submit__prevent="@post(...)"
func OnInput(fn func(*Ctx)) h.Attr
func OnChange(fn func(*Ctx)) h.Attr
func OnKeydown(k Key, fn func(*Ctx)) h.Attr   // widens viaCallNames — note it

type Key uint8
const ( Enter Key = iota; Esc; Tab; /* … closed enum, unexported backing */ )

// Numeric verbs via a separate handle (Go can't constrain a method's receiver
// type param). REPLACES Num (a per-verb-per-type tax).
type Counter struct{ Signal[int] }    // keep int-only for 1.0; Number-generic later
func (c *Counter) Op(ctx *Ctx) counterOps
type counterOps struct{ s *Signal[int]; ctx *Ctx }
func (o counterOps) Inc(); func (o counterOps) Dec(); func (o counterOps) Add(d int)

// state.go — append sugar for the chat log.
func (s *State[T]) Append(ctx *Ctx, v T)   // where T is a slice; s.Set(ctx, append(s.val, v))
```

**Two corrections the sub-memos got wrong — honor them:**
- **`Op`/`State.Set`/`Append` from an action are no-ops on a live island UNTIL section 1 lands.** They mutate the per-request throwaway today. Section 1 (rebind-on-island-inst) is a *precondition*, not a parallel track. Sequence it first.
- **Datastar modifier syntax must be browser-verified, not taken from memory.** The lesson at via.go:206 is "colon for the event" (`data-on:submit`). Whether the prevent modifier is `__prevent` or `.prevent` is exactly the class of bug `vt` cannot see — confirm against the bundled `datastar.js` in a real browser before wiring `OnSubmit`/`OnKeydown`. Do not ship on the spelling above unverified.

**Guarantees intact:** `On*` take named method values (lint-enforced); `Counter.Op(ctx)` keeps numeric verbs off the bare `Signal` surface; `Key` is a closed enum (no string event names); `Append` is one-line `Set` sugar. No reflection, no `&`, no user strings, no `any`.

---

## Sequencing & files of record

1. **Routing** — `via.go` (SSE/POST handlers, registry), `live.go` (lift `pulse`, always-loop). Red test: live POST mutates this connection's `State`, SSE frame reflects it; `-race`-clean under delete-then-POST interleave. **chromedp-gate this** — the tab-id/header round-trip is precisely what the `vt` harness has lied about.
2. **Compose** — new `compose.go`, `Ctx` path cursor, extend `viaCallNames`.
3. **Sugar** — `signals.go`, `state.go`; `Local` + `On*` + `Counter`; browser-verify every Datastar modifier.
4. **Chat showcase** — `Each` log + `OnSubmit` write-back + `topic` fan-out; the minimal app that exercises all four. (Flag the latent self-block: `Send` on the island goroutine calling `topic.Publish` while that same goroutine drains its own sub — drop-on-full makes it safe, but the sender's echo arrives via fan-out, not synchronously; document it.)

Files of record: `/home/jgonc/Personal/repos/via-v2/via.go`, `/home/jgonc/Personal/repos/via-v2/live.go`, `/home/jgonc/Personal/repos/via-v2/state.go`, `/home/jgonc/Personal/repos/via-v2/signals.go`, `/home/jgonc/Personal/repos/via-v2/h/h.go`, `/home/jgonc/Personal/repos/via-v2/csp.go`, `/home/jgonc/Personal/repos/via-v2/guarantee_test.go`, new `/home/jgonc/Personal/repos/via-v2/compose.go`.
---

I have full ground truth. The build state confirms slices 1–6 done; the keystone (action routing) and composition/sugar/showcase remain. Writing the ordered build sequence.

---

# CHAIR'S ORDERED BUILD SEQUENCE — via/v2 to feature-complete

This is the binding build plan. It accounts for what is already DONE (slices 1–6: hardened stateless core, `Signal[T]` handle-identity binding, live islands + per-tab SSE, `State[T]`, `via/topic` + `Subscribe`/`OnDispose`). Everything below is net-new, sequenced by hard dependency. Build each slice with `tdd-rygba`. The red test for each is stated.

## Minimal feature-complete bar

The flagship **live multi-user chat** (SHOWCASE memo's `Chat` struct) compiles, runs, and is browser-verified to do all four: (1) a message typed in one tab fans out to every connected tab, (2) the composer clears on send, (3) the "N online" presence header tracks connect/disconnect, (4) the message log renders as a keyed list that morphs correctly on append. The app code stays inside the envelope: zero JS, zero `&`, zero identifier strings, zero call-site closures, no `any`, pure ctx-free `View()`.

## Critical path

**Slice 1 (action routing) is the keystone and gates everything interactive.** The strict critical path to "showcase fully functional" is:

> **1 (routing) → 2 (Each/keyed lists) → 3 (Local + On* sugar) → 6 (chat) → 7 (browser gate)**

Slices 4 (compose If/When/Embed) and 5 (theme/CSP) are showcase-quality, not showcase-blocking — they run in parallel once 1 lands. Do not start 6 until 1, 2, 3 are green.

---

## Slice 1 — Live-island action routing (the tab-id handshake) — KEYSTONE

**Depends on:** nothing new (slices 4–6 done). **Browser-gate: YES (mandatory).**

The blocker: POST does `inst := root` (via.go:269/292/346) on a fresh copy, so an action cannot mutate *this connection's* island `State`. Resolution per the verdicts:

- **Lift `pulse` out of `runLiveStream`.** Today `pulse := make(chan func(*Ctx))` is a local (live.go:104) and never escapes. Create it in the GET/SSE handler; pass it in: `runLiveStream(reqCtx, island, inst, pulse, push)`. **Drop the no-ticks/no-subs early-return** — an interactive-only island has neither but must still loop and accept dispatched actions.
- **Per-app registry** (`map[string]*liveConn` guarded by a mutex; a local in `Register`, never global). `liveConn{ inst viewer; pulse chan func(*Ctx); done <-chan struct{} }`.
- **Tab id rides a request header, not a signal.** SSE handler mints `id := genTabID()` (128-bit, reuse `csp.go`'s `crypto/rand` minting), pushes one `datastar-patch-signals` frame `{"_viatab":"<id>"}` before the loop, registers the conn. `OnClick`/`On*` emit `@post('/_via/a/N',{headers:{'X-Via-Tab':$_viatab}})`. **Reason a header, not a body signal:** `shapeMatches` (via.go:61) requires `len(order)==len(in)`; a `_viatab` in the body but not in `order` 410s every live POST.
- **POST live branch:** read `X-Via-Tab` → `reg.get(id)`; miss → **410**. On hit, **skip `shapeMatches`** (the island re-render is the authority) and dispatch a fn that **re-binds against the island `inst`, never the throwaway**:
  ```go
  ok := lc.Dispatch(func(ic *Ctx) {
      bind, _ := renderRoot(lc.inst, in, true)
      if n >= 0 && n < len(bind.actions) { bind.actions[n]() }
  })
  // ok → 204 (push() ships the patch over SSE); !ok → 410
  ```
- **Three mandatory hazards, all closed:** (a) `Dispatch` send is `select { case pulse<-fn: case <-done: }` — never bare, or a POST racing a closed tab blocks forever on the unbuffered channel; (b) `reg.del(id)` is `defer`'d in the SSE handler alongside disposers so a panic can't leak the entry; (c) document that `State` resets on reconnect.

**Deliverable:** `via.go` (registry, SSE-mint, POST live branch), `live.go` (lifted `pulse`, always-loop), `genTabID`, `writeSignalsFrame`.
**Red test:** a live POST mutates this connection's `State` and the SSE frame reflects it; `-race`-clean under a delete-then-POST interleave.
**Browser-gate rationale:** the `X-Via-Tab` round-trip and the `_viatab` local-signal are exactly the plumbing the `vt`/unit harness has historically lied about (underscore-signal never-sent, dash-vs-colon syntax). Drive it headless in `vtbrowser/`.

## Slice 2 — Keyed lists: `Each` + list tags

**Depends on:** Slice 1 (a list of *interactive* rows needs island routing; an append-only log needs the action cursor to renumber soundly). **Browser-gate: YES (keyed morph).**

- `func Each[T any](items []T, key func(T) string, row func(T) h.H) h.H` — `key` returns **app data** (a message id), not a via-minted string.
- **Action-index soundness under list growth:** action ids are positional (`ActionSlot`, via.go:108), so a growing/reordering list renumbers the table. Add a per-path render cursor in `Ctx`: `pathSeg{ tag uint32; ord uint32; localOrd uint32 }` with `PushPath`/`PopPath`; `ActionSlot` folds path-stack + the **top seg's** `localOrd++` (the counter lives ON the seg, saved/restored with it — not a global int, or a sibling `Dyn` that pushed/popped silently misroutes).
- Extend the lint whitelist: `Each`/`When` take builder funcs → add to `viaCallNames` arg-position carve-outs (`guarantee_test.go`'s `map[string]bool`). A named-method-value row builder (`c.row`) already passes; a literal trips — intended.
- Add `h.Ul`/`h.Li`/`h.B` tags to `h/h.go` if absent.
- `EachIndexed` **rejected for rows carrying actions** (index keys + actions = renumber footgun). Ship `Each` (mandatory key) only for 1.0.

**Deliverable:** new `compose.go`, `Ctx` cursor, `h/h.go` tags, lint extension.
**Red test:** appending to the list adds one keyed `<li>` and morphs by id without disturbing sibling action routing.
**Browser-gate rationale:** Datastar's by-id morph on keyed fragments is unspecced — verify the keyed-fragment strategy in a real browser, not asserted DOM strings.

## Slice 3 — `Local[T]` + event/numeric ergonomics sugar

**Depends on:** Slice 1 (the `On*` helpers emit the tab-id header; `Op`/`State.Set`/`Append` from an action are no-ops on a live island until Slice 1 lands). **Browser-gate: YES (Datastar modifier spellings).**

- **`Local[T]` as a `Signal[T]` subtype** (`struct{ Signal[T] }`) — inherits `Bind()`/`Display()`, overrides `bind()` to emit a `_`-prefixed slot and skip `SignalInit` hydration. No `Get`/`Set` exported (compiler-enforced client-only). The `_` prefix means Datastar never POSTs it → naturally absent from `in`, never disturbs `shapeMatches`. Add `Show(*Local[bool])` and `Class(name, *Local[bool])`.
- **Event helpers** (`OnSubmit`/`OnInput`/`OnChange` already lint-reserved): `func OnSubmit(func(*Ctx)) h.Attr`, etc. `OnKeydown(Key, func(*Ctx))` widens `viaCallNames` — note it. `Key` is a **closed enum** (`type Key uint8; const (Enter Key = iota; Esc; …)`), no string event names.
- **Numeric verbs via a separate handle** — drop `Num` (via.go:183, a per-verb-per-type tax). `type Counter struct{ Signal[int] }`; `func (c *Counter) Op(ctx *Ctx) counterOps` with `Inc/Dec/Add`. Keep int-only for 1.0.
- `func (s *State[T]) Append(ctx *Ctx, v T)` — one-line `Set(ctx, append(...))` sugar for the chat log.
- **Browser-verify every Datastar modifier before shipping** (`__prevent` vs `.prevent`, key names) against the bundled `datastar.js` — the class of bug unit tests cannot see.

**Deliverable:** `signals.go` (`Local`, `On*`, `Counter`, `Key`), `state.go` (`Append`).
**Red test:** `Local[bool]` toggle shows/hides client-side with zero server round-trip; `OnSubmit` posts without a hard navigation.

## Slice 4 — Composition: `If` / `When` / `Embed` (parallel, showcase-quality)

**Depends on:** Slice 2's path cursor (shares the same renumber machinery). **Browser-gate: NO** (structural; unit-coverable). Not on the critical path — chat needs only `Each`.

- `func If(cond bool, node h.H) h.H` — eager, no closure, never trips the lint.
- `func When(cond bool, build func() h.H) h.H` — lazy branch; whitelist the builder arg.
- `func Embed[T any, PT interface{ *T; viewer }](child T) h.H` — nested composition, same `PT` sentinel as `Register`, child by value (auto-addressed, zero `&`). Already in the lint whitelist.

**Red test:** toggling `cond` adds/removes a branch and the action table renumbers without misroute.

## Slice 5 — `WithTheme()` + the `style-src` CSP edit (parallel, polish)

**Depends on:** nothing. **Browser-gate: light** (visual sanity once chat exists). Not blocking.

- `via.WithTheme()` functional option injects a ~30-line embedded classless `<style nonce>` styling bare semantic tags (`body h1 ul li form input button`). Keeps `View()` class-free.
- **Required CSP edit:** `csp.go` emits no `style-src` today (default-src covers it but not a nonce'd inline `<style>`). Add `style-src 'self' 'nonce-…'` and surface the style nonce. This is the one real framework edit the polish requires.

## Slice 6 — The flagship chat showcase

**Depends on:** Slices 1, 2, 3 (and 4/5 for polish). **Browser-gate: YES — this IS the acceptance test.**

Build `example/chat/main.go` exactly as the SHOWCASE memo's `Chat` struct: `Room{bus, presence *topic.Topic}`, `OnConnect` subscribes to both + `join`/`part`, `Send` publishes + clears `Draft`, `View` renders `Online.Display()` + `Each(Log, idOf, row)` + the composer. `Who` is `Local[string]`, `Draft` is `Signal[string]`, `Log`/`Online` are `State`.
**Flag the latent self-block (document, don't fix):** `Send` on the island goroutine calls `topic.Publish` which fans out to this island's own sub buffer; drop-on-full makes it safe, but the sender's echo arrives via fan-out, not synchronously.
**Acceptance (browser, two tabs):** type in tab A → appears in both; composer clears; "2 online" → "1 online" on tab close; log morphs by id.

## Slice 7 — Final browser-verification + docs

**Depends on:** Slice 6. **Browser-gate: YES.**

Full `vtbrowser/` headless run of the chat acceptance criteria; `-race ./...` green; ROADMAP build-log updated; document the three behaviors (State-resets-on-reconnect, header-CSRF-floor layering, sender-echo-via-fanout).

---

## Dependency graph (at a glance)

```
1 (routing, browser) ─┬─> 2 (Each, browser) ─┬─> 6 (chat, browser) ─> 7 (final browser)
                      ├─> 3 (Local/On*, browser) ┘
                      └─> 4 (If/When/Embed)   [parallel, not blocking]
   5 (theme/CSP)  [independent, parallel, polish]
```

**The one-line truth:** Slice 1 is the whole game. Once a POST reaches the connection's island through the lifted `pulse` channel, via is genuinely interactive and the SHOWCASE `Chat` code becomes real; 2–7 are the list/sugar/polish that make it read like the counter example.

**Files of record:** `/home/jgonc/Personal/repos/via-v2/via.go`, `/home/jgonc/Personal/repos/via-v2/live.go`, `/home/jgonc/Personal/repos/via-v2/state.go`, `/home/jgonc/Personal/repos/via-v2/signals.go`, `/home/jgonc/Personal/repos/via-v2/h/h.go`, `/home/jgonc/Personal/repos/via-v2/csp.go`, `/home/jgonc/Personal/repos/via-v2/guarantee_test.go`, new `/home/jgonc/Personal/repos/via-v2/compose.go`, new `/home/jgonc/Personal/repos/via-v2/example/chat/main.go`, `/home/jgonc/Personal/repos/via-v2/vtbrowser/`.