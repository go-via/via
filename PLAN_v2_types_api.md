# Via v1: Compositions as Types

> This document describes the new **v1 API** for Via — a complete rewrite
> where compositions are typed structs, signals/state are struct fields,
> and actions are methods. The v1 API lives in `github.com/go-via/via/v1`.
> The current root package (`github.com/go-via/via`) remains as v0.2.x.

## Context

The existing v0.2.x API registers pages and components as closures over a
runtime `*Cmp` object:

```go
app.Page("/counter", func(cmp *via.Cmp) {
    count := via.State(cmp, 0)
    inc := cmp.Action(func(ctx *via.Ctx) error {
        count.Set(ctx, count.Get(ctx)+1); return nil
    })
    cmp.View(func(ctx *via.Ctx) h.H {
        return h.Div(h.Text(fmt.Sprint(count.Get(ctx))), inc.OnClick())
    })
})
```

This is flexible but leans on hidden machinery: state/signal IDs are generated
from `runtime.Callers` stack frames, action IDs are random strings, per-request
work lives in fresh maps, and the render path fully re-executes the view closure
on every state change. No `sync.Pool` exists anywhere.

The goal of v1 is to replace that model with one where **a composition is a
Go struct the user defines**, signals/state are **typed struct fields**,
actions are **methods**, and IDs are **deterministic** (field/method names).
Along the way we cash in the allocation wins that the old closure-driven
design blocked:

1. Per-request arena pooled in `sync.Pool`.
2. Typed `Signal[T]` / `State[T]` with direct field access via unsafe offsets — no map lookup, no reflection, no JSON marshal on unchanged reads.
3. Fixed-size dirty bitset for the patch queue instead of `map[string]any`.
4. Pre-flattened middleware chain per descriptor.
5. `sync.Pool[*bytes.Buffer]` on every write path.

Target feel: reading a `v1` composition should feel like reading a normal Go struct with methods. Decisions confirmed with the user:

- **Fully replace** the old API in v1 (delete `Cmp` as a runtime object handed to user closures, `App.Page(func(*Cmp))`, `runtime.Callers` ID trick, `genRandID`, functional `Signal(cmp, init)` / `State(cmp, init)`).
- **Typed + pooled arena** runtime (no pre-compiled render plan in this branch — that's a follow-up once the shape settles; the API is designed to permit it later).
- **Field name as key, tag overrides** (`N v1.Signal[int]` → key `"n"`; add `` `v1:"count"` `` to override).
- **One type name, two roles**: `v1.Composition` is the interface; mounting at a route makes it "a page"; nesting inside another's view makes it "a component". No separate `Page` type.

## Final shape (user-facing)

```go
package ui

import "github.com/go-via/via/v1"

type Counter struct {
    ID    int `path:"id"`                     // decoded from /counter/{id}

    N    v1.Signal[int]                      // client-side, key "n"
    Step v1.Signal[int] `v1:"step,init=1"`    // client-side with initial
    Hits v1.State[int]                      // tab-scoped server state, key "hits"

    // Nested compositions as fields. Parent passes signals/state into them
    // by shared field handles (untagged -> pass-through).
    Chart Chart
}

func (c *Counter) Init(ctx *v1.Ctx) error { /* optional */ return nil }

func (c *Counter) Inc(ctx *v1.Ctx) error {
    c.Hits.Set(ctx, c.Hits.Get(ctx)+c.Step.Get(ctx))
    return nil
}

func (c *Counter) View(ctx *v1.Ctx) h.H {
    return h.Div(
        h.H1(h.Text("Counter")),
        h.P(h.Text("Count: "), c.Hits.Text()),
        h.Input(h.Type("number"), c.Step.Bind()),
        h.Button(h.Text("+"), v1.OnClick(c.Inc)),
        c.Chart.View(ctx),
    )
}

// wire:
func main() {
    app := v1.New()
    v1.Mount[ui.Counter](app, "/counter/{id}")
    app.Start()
}
```

## Design

### The `Composition` type

- `v1.Composition` — exported interface requiring `View(ctx *Ctx) h.H`. Pages and components both satisfy it; there is no second type.
- Optional hooks detected by interface assertion at registration: `Init(ctx *Ctx) error`, `Dispose(ctx *Ctx)`.
- `v1.Mount[C any](app *App, route string)` and `v1.MountOn[C any](g *Group, route string)` — generic factories that mount a root composition at a route. `C` must be a struct; the constraint is `*C` satisfies `Composition`. Panics at registration if `View` is missing, if a `path:"x"` tag has no matching route segment, or if a field type is disallowed.
- **Fresh `*C` per request**: root compositions are pooled via `sync.Pool` keyed by the `cmpDescriptor`. Fields are reset to zero (except typed signal/state handles whose internal pointer is re-bound to the per-request arena).
- **Nested compositions (a.k.a. "components")** are the same interface. A parent holds a child as a field (value or pointer) and calls `child.View(ctx)` inside its own `View`. Child signal/state fields that are **untagged** act as pass-through handles to whatever the parent assigned; **tagged** fields register the child's own reactive state. Child descriptors are built recursively at `Mount` time by walking composition-typed fields.

### Registration: build `cmpDescriptor` once

On `via.Mount`, reflect over `reflect.TypeOf(*C)` once and cache:

```go
type cmpDescriptor struct {
    typ         reflect.Type
    route       string
    paramSlots  []paramSlot    // {name, fieldOffset, kind}
    signalSlots []signalSlot   // {name, fieldOffset, kind, encoder, initRaw}
    stateSlots  []stateSlot    // {name, fieldOffset, kind, scope}
    actionSlots []actionSlot   // {name, methodIndex, invoke}
    actionByID  map[string]int // wire id -> actionSlots index
    childSlots  []childSlot    // nested composition-typed fields (recursive descriptors)
    mwChain     []Middleware   // pre-flattened (app + group + composition)
    cmpPool     sync.Pool      // pooled *C
    rsPool      sync.Pool      // pooled *requestState sized for this descriptor
}
```

- `signalSlot.encoder` is chosen at build time by `reflect.Kind`: scalars use `strconv.Append*`; strings use a quote-aware appender; composites use `json.Marshal` with the result **cached in the request arena** and invalidated only when the signal's generation counter advances.
- IDs:
  - **Local id** = `reflect.StructField.Name` lowercased, override via `via:"custom"`.
  - **Fully-qualified id (the wire key)** = field-path from the mounted root joined with `.`. The root composition's own fields have no prefix. Example below.
  - Action id = method name, qualified by the owning composition's field path: `Inc` on the root, `Chart.Refresh` on a nested `Chart` field. Tests address by qualified name; wire uses `/_action/{qualifiedID}`.
- `panic` at `Mount` time for: missing `View`, duplicate local id after tag override, unsupported field type, `path:"x"` without a matching `{x}` segment, conflicting option sets. Qualified ids cannot collide by construction — two sibling compositions of the same type live at different field paths.

#### Collision-free keying on nested compositions

Two `Counter` instances under the same parent would both declare a local signal `n`. The descriptor tree walk gives each a distinct wire key by its field path:

```go
type Dashboard struct {
    A Counter              // A.n, A.step, A.hits, A.Inc
    B Counter              // B.n, B.step, B.hits, B.Inc
    Chart Chart            // Chart.title, Chart.Refresh
}
```

Rules:
- The wire key for a slot is `<parentFieldPath>.<localID>` where the root has no parent path. At the root, `N via.Signal[int]` stays just `"n"` — short keys are preserved for the common flat case.
- **Untagged pass-through fields on a child** (e.g. `Series via.Signal[int]` with the parent assigning `child.Series = parent.Visits`) are **not re-registered** — the child borrows the parent's slot, same wire key, no new cell.
- Depth is a compile-time-known property of the struct tree, so the qualified id is computed once at `Mount`, cached on the slot, and written as a constant byte slice on every SSE frame (no per-request string concat).
- Slice/map fields of compositions are **not supported in this branch** — dynamic-length composition collections would need an index-suffix scheme (`Items[0].n`) that complicates the one-time reflection walk. Deferred to a follow-up; static struct composition covers the target use cases.
- Tag override still works: `` `via:"count"` `` changes the local id only; the qualifier comes from the field path.

### Typed signals / state

Exported generic handle types, zero-value usable (convention: unexported concrete, exported factory — here the handle *is* the exported type; its fields are unexported and only bound at registration):

```go
type Signal[T any] struct {
    slot uint16              // filled at Mount
    rs   *requestState       // nil until bound per-request
}

func (s Signal[T]) Get() T           // typed read via unsafe offset into rs.page
func (s Signal[T]) Set(v T)          // typed write + bitset dirty
func (s Signal[T]) Bind() h.H        // client-side two-way binding attribute
func (s Signal[T]) Text() h.H        // reactive text node
func (s Signal[T]) Show() h.H        // data-show attr
```

- `State[T]` same shape, tab-scoped. `UserState[T]` session-scoped. `AppState[T]` app-scoped. Scope is encoded in the *type*, so `ctx.foo.Set(v)` with the wrong scope is a compile error, replacing today's `WithScopeApp()` option.
- Handles do **not** carry the value; the value lives in the composition struct field (for Signals/Tab State) or in the app/user registry (for App/User State). Reads use `unsafe.Add(rs.cmp, slot.fieldOffset)` + generic type assertion. **Zero reflection on the hot path.**
- Per-request storage for signals is a fixed-size slice on `*requestState`, indexed by `slot` — not a map.

### Actions as methods

- At `Mount`, enumerate exported methods on `*C` with signature `func(*v1.Ctx) error`. Each becomes an `actionSlot` with a reflect-built thunk `invoke(cmp unsafe.Pointer, ctx *Ctx) error`. Methods on nested composition types get their own descriptors' action tables.
- `v1.OnClick(m)` / `v1.OnChange(m)` / `v1.OnKeyDown(key, m)` / `v1.OnSubmit(m)` take `func(*v1.Ctx) error` as a method value. Implementation: `runtime.FuncForPC(reflect.ValueOf(m).Pointer()).Name()` → strip to method name → look up `actionByID[name]` on the owning descriptor. Panics at render time if the method wasn't registered (unexported, wrong signature).
- Testing calls the method directly: `c := &Counter{}; ctx := v1.NewTestCtx(t, c); require.NoError(t, c.Inc(ctx)); assert.Equal(t, 1, c.Hits.Get())`.
- Wire path: `POST /_action/{cmpID}/{methodName}`. One map lookup per request for dispatch (acceptable — network-bounded).

### Per-request arena (`*requestState`)

```go
type requestState struct {
    descriptor *cmpDescriptor
    cmp        unsafe.Pointer   // *C bound for this request (root composition)
    signals    []signalCell     // len = len(descriptor.signalSlots) across the tree
    states     []stateCell      // tab-scoped; user/app go through registries
    params     []string         // len = len(descriptor.paramSlots)
    queue      patchQueue       // embedded, not pointer
    session    *session
    w          http.ResponseWriter
    r          *http.Request
    done       chan struct{}
    disposed   atomic.Bool
}

type signalCell struct {
    gen     uint64
    encGen  uint64
    encoded []byte
}

type patchQueue struct {
    mu       sync.Mutex
    dirty    bitset           // fixed size = signal count
    elemsBuf *bytes.Buffer    // pooled; last-wins element patch
    scripts  strings.Builder
    redirect string
    wake     chan struct{}
}
```

- `rsPool.Get()` returns a pre-sized `*requestState`; `Reset()` clears dirty + rewinds buffers.
- `Ctx` becomes a thin wrapper over `*requestState` (keeps the existing name so most `h` helpers are unchanged).

### Pools

- `renderBufPool` — `sync.Pool[*bytes.Buffer]` with 8KB starting capacity. Used by `flushPatches`, `SyncElements`, SSE frame writes.
- `ssePool` — `sync.Pool[*sseWriter]` with a 4KB scratch; frame prefixes are constant byte slices (no `fmt.Sprintf`).
- `cmpPool` per descriptor (pooled root composition `*C`).
- `rsPool` per descriptor (sized by signal/state/param counts).

### Middleware & route params

- Descriptor stores a flat `mwChain` built once at registration from app + group + page middleware. No per-request `append`.
- Path params decoded into struct fields by descriptor `paramSlots`: `strconv` for int/uint/float/bool, direct assignment for string. No per-request `map[string]string`.

### Plugins

- `via.Plugin` interface unchanged: `Register(*App)`. `Plugin()` constructor convention unchanged.
- Existing plugins (`picocss`, `echarts`) port to the new API: app-level state uses `via.AppState[T]`; plugin-mounted pages use `via.Mount[P](app, ...)`.

### Testing

- `via.NewTestCtx(t testing.TB, p Page, opts ...TestOption) *Ctx` — instantiates the page through the same descriptor path, returns a bound `*Ctx`. Supports `WithPathParams(...)`, `WithSession(...)`.
- `testclient` drives actions by name: `tc.Action("Inc").Fire()` / `tc.Signal("step").Set(3)` — no regex extraction. Internally uses the descriptor's `actionByID` / `signalByID` tables exposed through an internal test hook.

### Exported surface: organized by concern (sub-packages)

A single flat `v1.*` namespace crowds fast. The new surface splits along themes so every call-site reads like prose. Each sub-package is small, focused, and the import line pays for itself by giving the dev short names at the point of use:

```go
import (
    "github.com/go-via/via/v1"
    "github.com/go-via/via/v1/on"
    "github.com/go-via/via/v1/scope"
    "github.com/go-via/via/h"
)
```

**`v1` (root) — the things every composition file touches**

- Types: `Composition` (interface), `Ctx`, `Signal[T]`, `State[T]` (tab-scoped — the common case), `App`, `Plugin`.
- Factories: `New(opts ...Option) *App`, `Mount[C any](app *App, route string)`, `MountOn[C any](g *Group, route string)`.
- App options: `WithPlugins(...)`, plus existing app-level helpers.
- App methods retained: `Use`, `Group`, `AppendToHead`, `AppendToFoot`, `AppendAttrToHTML`, `HandleFunc`, `Start`, `Shutdown`.

**`v1/on` — event bindings**

Short verbs at the call-site; `on.Click(c.Inc)` reads like HTML:

- `on.Click(m, opts ...TriggerOption) h.H`
- `on.Change(m, opts ...TriggerOption) h.H`
- `on.Input(m, opts ...TriggerOption) h.H`
- `on.Submit(m, opts ...TriggerOption) h.H`
- `on.Key(key string, m, opts ...TriggerOption) h.H`
- `on.SetSignal[T any](sig Signal[T], v T) TriggerOption` — trigger option for bundled signal writes.

**`v1/scope` — non-tab state scopes**

Tab-scoped state is the default (lives at `v1.State[T]`). Wider scopes are explicit:

- `scope.User[T any]` — session-scoped state, survives across tabs in one session.
- `scope.App[T any]` — app-scoped state, shared across all sessions.

`c.Hits.Set(...)` is already scope-typed — swapping `v1.State[int]` for `scope.User[int]` is a one-token change and a compile-time scope check.

**`v1/test` — testing helpers**

- `test.NewCtx(t testing.TB, c v1.Composition, opts ...CtxOption) *v1.Ctx` — direct-method testing.
- `test.Client(t testing.TB, app *v1.App, opts ...ClientOption) *Client` — replaces the ad-hoc `testclient` with a name-addressed client: `tc.Action("Inc").Fire()`, `tc.Signal("step").Set(3)`.
- `test.WithPathParams(...)`, `test.WithSession(...)` — option helpers.

**`h` — HTML builder (unchanged)**

Stays as-is. `Signal[T]` gains `.Bind()`, `.Text()`, `.Show()` methods that return `h.H` so the builder and the reactive primitives compose without a third package.

**Plugin constructors (unchanged convention)**

Every plugin package exposes `Plugin()` as its public constructor (per CONVENTIONS.md). Usage stays `v1.New(v1.WithPlugins(picocss.Plugin(), echarts.Plugin()))`.

**Read like a Go dev who's never seen it**

```go
import (
    "github.com/go-via/via/v1"
    "github.com/go-via/via/v1/on"
    "github.com/go-via/via/h"
)

type Counter struct {
    Hits v1.State[int]
    Step v1.Signal[int] `v1:"step,init=1"`
}

func (c *Counter) Inc(ctx *v1.Ctx) error {
    c.Hits.Set(ctx, c.Hits.Get(ctx)+c.Step.Get(ctx))
    return nil
}

func (c *Counter) View(ctx *v1.Ctx) h.H {
    return h.Div(
        h.P(h.Text("Count: "), c.Hits.Text()),
        h.Input(h.Type("number"), c.Step.Bind()),
        h.Button(h.Text("+"), on.Click(c.Inc)),
    )
}
```

### Removed

- `Cmp` and all its methods (`View`, `Action`, `Init`, `Dispose`, `Component`, `Content`).
- `App.Page(route, func(*Cmp))`.
- Functional `Signal(cmp, init)` / `State(cmp, init, opts...)` / `AppSignal(v, ...)`.
- `runtime.Callers`-based state ID deduplication.
- `genRandID` for signals/actions.
- `ctx.GetPathParam` (replaced by struct-tag decode).
- Options like `WithScopeApp`, `WithScopeUser` (scope is now the handle type in `v1/scope`).
- Root-level `v1.OnClick`/`OnChange`/`OnInput`/`OnKeyDown`/`OnSubmit`/`ActionWithSetSignal` (moved under `v1/on`).
- Root-level `v1.UserState[T]`/`v1.AppState[T]` (moved under `v1/scope` as `scope.User[T]`/`scope.App[T]`).
- Root-level `v1.NewTestCtx` (moved under `v1/test`).

## Critical files

**v1 package files** (new directory `v1/`):

- `v1/composition.go` — `Composition` interface, `cmpDescriptor`, reflection walk, pool setup, child-slot rendering, field-path qualified ids.
- `v1/signal.go` — typed `Signal[T]` handle + cell storage.
- `v1/state.go` — typed `State[T]` (tab only). User/App scopes live in `v1/scope`.
- `v1/action.go` — action-slot dispatch + `TriggerOption` type used by `v1/on`.
- `v1/runtime.go` — `requestState`, pools, bitset patch queue, descriptor-driven mount.
- `v1/app.go` — registry of descriptors, numeric `id` context registry, pool wiring.
- `v1/group.go` — `MountOn[C]` + middleware flatten.
- `v1/middleware.go` — no per-request copy; descriptor holds flat chain.
- `v1/config.go` — drop scope options.

**New sub-packages within v1/:**

- `v1/on/on.go` — `Click`, `Change`, `Input`, `Submit`, `Key`, `SetSignal`.
- `v1/scope/scope.go` — `User[T]` and `App[T]` scoped state handles.
- `v1/test/test.go` — `NewCtx`, `Client`, `WithPathParams`, `WithSession`.

**New internal files within v1/:**

- `arena.go` — `requestState` + `sync.Pool` lifecycle.
- `bench_test.go` — `-benchmem` ceilings gated in `ci-check.sh`.
- `internal/shared/` — shared cell/encoder primitives used by both v1/state and v1/scope.

**Tests migrated:**

- All existing `*_test.go` files in v1/ get ported to the new API.

**Touched but not restructured:**

- `h/` — unchanged surface; adapts `Signal.Text()` / `Signal.Bind()` callers.
- `plugins/picocss/` and `plugins/echarts/` — port to typed API.
- `sess.go` — keep; `UserState[T]` builds on it.

## Implementation order (test-first, per CONVENTIONS.md)

Each step: write failing test against the public API → implement → green → commit.

1. Create `v1/` sub-package. Copy current `*_test.go` files as baseline.
2. `Composition` interface + `Mount[C]` happy path with a struct that has only `View`. Descriptor build + pooled `*C` per request. Test: `TestMount_rendersComposition`.
3. `path:"x"` tag decoding for `int`, `string`. Test: `TestMount_decodesPathParams`.
4. `Signal[T]` typed read/write with field-offset storage; scalar encoder. Test: `TestSignal_typedGetSet`, `TestSignal_initFromTag`.
5. Action method discovery + `v1.OnClick(method)`. Test: `TestAction_firesMethodByName`.
6. `State[T]` tab-scoped + reactive re-render on `Set`. Test: `TestState_reRendersOnSet`.
7. `UserState[T]`, `AppState[T]`. Test: scope coherence across tabs/sessions.
8. Nested compositions as fields; pass-through signal handles. Test: `TestComposition_childInheritsParentSignal`.
9. Per-request `sync.Pool` arena + bitset patch queue. Test: `TestRequestState_resetsBetweenRequests`; bench: `BenchmarkCounterAction_zeroAllocs`.
10. Buffer pools on every write path. Bench: `BenchmarkCounterRender`.
11. Composite-typed signal with cached encoder. Bench: `BenchmarkCompositeSignal_encodesOncePerChange`.
12. Lifecycle: `Init`/`Dispose` detection + tab disposal hooks. Test: `TestComposition_disposeRunsOnTabClose`.
13. Port `picocss` and `echarts` plugins. Integration test: existing plugin behavioral tests pass against new API.
14. Port existing examples in `internal/examples/**` (counter, counter-comp, etc.).
15. Replace `testclient` surface with name-addressed helpers. Migrate all `*_test.go` files to new addressing.
16. Delete dead code: old `Cmp` runtime handle, `App.Page(func(*Cmp))`, functional `Signal/State` constructors, `runtime.Callers` ID logic, `genRandID`. Confirm `go vet ./...` + `go test -race ./...` stay green.
17. Add `-benchmem` thresholds to `ci-check.sh`. Any regression past target fails CI.

## Verification

- `go test -race ./...` — full suite green; all existing behavioral coverage ported to the new API (test names follow the `TestSubject_behavior` convention).
- `go test -bench=. -benchmem ./...` — `BenchmarkCounterAction_zeroAllocs` asserts **0 allocs/op** for a scalar-signal bump in steady state; `BenchmarkCounterRender` asserts bounded allocs/op (buffer rental only).
- Manual smoke: run `internal/examples/counter` and `internal/examples/countercomp`, open two browser tabs, confirm tab-scoped and app-scoped state behave per the expected differentiation.
- Plugin smoke: run an example that uses `picocss` and `echarts`; confirm theme switching + chart updates work end-to-end.
- `ci-check.sh` exits non-zero on any alloc regression past configured thresholds.
