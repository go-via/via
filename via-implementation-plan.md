# Via: Compositions as Types

> This document describes the new **typed composition API** for Via —
> a complete rewrite where compositions are typed structs, signals/state
> are struct fields, and actions are methods.
>
> Reference: `github.com/go-via/via/internal/viaold` (v0.2.x)
> Implementation: `github.com/go-via/via` (new, built from scratch)

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
from `runtime.Callers` stack frames, action IDs are random strings,
per-request work lives in fresh maps, and the render path fully re-executes
the view closure on every state change. No `sync.Pool` exists anywhere.

The goal is to replace that model with one where **a composition is a
Go struct the user defines**, signals/state are **typed struct fields**,
actions are **methods**, and IDs are **deterministic** (field/method names).
Along the way we cash in the allocation wins that the old closure-driven
design blocked:

1. Per-request arena pooled in `sync.Pool`.
2. Typed `Signal[T]` / `State[T]` with direct field access via unsafe offsets —
   no map lookup, no reflection, no JSON marshal on unchanged reads.
3. Fixed-size dirty bitset for the patch queue instead of `map[string]any`.
4. Pre-flattened middleware chain per descriptor.
5. `sync.Pool[*bytes.Buffer]` on every write path.

Target feel: reading a composition should feel like reading a normal Go
struct with methods. Decisions confirmed with the user:

- **Fully replace** the old API (delete `Cmp` as a runtime object handed to user
  closures, `App.Page(func(*Cmp))`, `runtime.Callers` ID trick, `genRandID`,
  functional `Signal(cmp, init)` / `State(cmp, init)`).
- **Typed + pooled arena** runtime (no pre-compiled render plan in this branch —
  that's a follow-up once the shape settles; the API is designed to permit
  it later).
- **Field name as key, tag overrides** (`N via.Signal[int]` → key `"n"`;
  add `` `via:"count"` `` to override).
- **One type name, two roles**: `via.Composition` is the interface; mounting at
  a route makes it "a page"; nesting inside another's view makes it "a
  component". No separate `Page` type.

## Final shape (user-facing)

```go
package ui

import "github.com/go-via/via"

type Counter struct {
    ID    int `path:"id"`                     // decoded from /counter/{id}

    N    via.Signal[int]                      // client-side, key "n"
    Step via.Signal[int] `via:"step,init=1"` // client-side with initial
    Hits via.State[int]                      // tab-scoped server state, key "hits"

    // Nested compositions as fields. Parent passes signals/state into them
    // by shared field handles (untagged -> pass-through).
    Chart Chart
}

func (c *Counter) Init(ctx *via.Ctx) error { /* optional */ return nil }

func (c *Counter) Inc(ctx *via.Ctx) error {
    c.Hits.Set(ctx, c.Hits.Get(ctx)+c.Step.Get(ctx))
    return nil
}

func (c *Counter) View(ctx *via.Ctx) h.H {
    return h.Div(
        h.H1(h.Text("Counter")),
        h.P(h.Text("Count: "), c.Hits.Text()),
        h.Input(h.Type("number"), c.Step.Bind()),
        h.Button(h.Text("+"), via.OnClick(c.Inc)),
        c.Chart.View(ctx),
    )
}

// wire:
func main() {
    app := via.New()
    via.Mount[ui.Counter](app, "/counter/{id}")
    app.Start()
}
```

## Design

### The `Composition` type

- `via.Composition` — exported interface requiring `View(ctx *Ctx) h.H`.
  Pages and components both satisfy it; there is no second type.
- Optional hooks detected by interface assertion at registration:
  `Init(ctx *Ctx) error`, `Dispose(ctx *Ctx)`.
- `via.Mount[C any](app *App, route string)` and `via.MountOn[C any](g *Group,
  route string)` — generic factories that mount a root composition at a
  route. `C` must be a struct; the constraint is `*C` satisfies `Composition`.
  Panics at registration if `View` is missing, if a `path:"x"` tag has no
  matching route segment, or if a field type is disallowed.
- **Fresh `*C` per request**: root compositions are pooled via `sync.Pool`
  keyed by the `cmpDescriptor`. Fields are reset to zero (except typed signal/state
  handles whose internal pointer is re-bound to the per-request arena).
- **Nested compositions (a.k.a. "components")** are the same interface. A parent
  holds a child as a field (value or pointer) and calls `child.View(ctx)`
  inside its own `View`. Child signal/state fields that are **untagged** act
  as pass-through handles to whatever the parent assigned; **tagged** fields
  register the child's own reactive state. Child descriptors are built
  recursively at `Mount` time by walking composition-typed fields.

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

- `signalSlot.encoder` is chosen at build time by `reflect.Kind`: scalars use
  `strconv.Append*`; strings use a quote-aware appender; composites use
  `json.Marshal` with the result **cached in the request arena** and invalidated
  only when the signal's generation counter advances.
- IDs:
  - **Local id** = `reflect.StructField.Name` lowercased, override via `via:"custom"`.
  - **Fully-qualified id (the wire key)** = field-path from the mounted root
    joined with `.`. The root composition's own fields have no prefix. Example
    below.
  - Action id = method name, qualified by the owning composition's field path:
    `Inc` on the root, `Chart.Refresh` on a nested `Chart` field. Tests address
    by qualified name; wire uses `/_action/{qualifiedID}`.
- `panic` at `Mount` time for: missing `View`, duplicate local id after tag
  override, unsupported field type, `path:"x"` without a matching `{x}` segment,
  conflicting option sets. Qualified ids cannot collide by construction — two
  sibling compositions of the same type live at different field paths.

#### Collision-free keying on nested compositions

Two `Counter` instances under the same parent would both declare a local
signal `n`. The descriptor tree walk gives each a distinct wire key by its
field path:

```go
type Dashboard struct {
    A Counter              // A.n, A.step, A.hits, A.Inc
    B Counter              // B.n, B.step, B.hits, B.Inc
    Chart Chart           // Chart.title, Chart.Refresh
}
```

Rules:

- The wire key for a slot is `<parentFieldPath>.<localID>` where the root has no
  parent path. At the root, `N via.Signal[int]` stays just `"n"` — short keys
  are preserved for the common flat case.
- **Untagged pass-through fields on a child** (e.g. `Series via.Signal[int]`
  with the parent assigning `child.Series = parent.Visits`) are **not
  re-registered** — the child borrows the parent's slot, same wire key,
  no new cell.
- Depth is a compile-time-known property of the struct tree, so the qualified id
  is computed once at `Mount`, cached on the slot, and written as a constant byte
  slice on every SSE frame (no per-request string concat).
- Slice/map fields of compositions are **not supported in this branch** —
  dynamic-length composition collections would need an index-suffix scheme
  (`Items[0].n`) that complicates the one-time reflection walk. Deferred to a
  follow-up; static struct composition covers the target use cases.
- Tag override still works: `` `via:"count"` `` changes the local id only; the
  qualifier comes from the field path.

### Typed signals / state

Exported generic handle types, zero-value usable (convention: unexported
concrete, exported factory — here the handle *is* the exported type; its fields
are unexported and only bound at registration):

```go
type Signal[T any] struct {
    slot uint16              // filled at Mount
    rs   *requestState       // nil until bound per-request
}

func (s Signal[T]) Get() T           // typed read via unsafe offset into rs.page
func (s Signal[T]) Set(v T)          // typed write + bitset dirty
func (s Signal[T]) Bind() h.H        // client-side two-way binding attribute
func (s Signal[T]) Text() h.H       // reactive text node
func (s Signal[T]) Show() h.H     // data-show attr
```

- `State[T]` same shape, tab-scoped. `UserState[T]` session-scoped. `AppState[T]`
  app-scoped. Scope is encoded in the *type*, so `ctx.foo.Set(v)` with the
  wrong scope is a compile error, replacing today's `WithScopeApp()` option.
- Handles do **not** carry the value; the value lives in the composition struct
  field (for Signals/Tab State) or in the app/user registry (for App/User State).
  Reads use `unsafe.Add(rs.cmp, slot.fieldOffset)` + generic type assertion.
  **Zero reflection on the hot path.**
- Per-request storage for signals is a fixed-size slice on `*requestState`,
  indexed by `slot` — not a map.

### Actions as methods

- At `Mount`, enumerate exported methods on `*C` with signature
  `func(*via.Ctx) error`. Each becomes an `actionSlot` with a reflect-built thunk
  `invoke(cmp unsafe.Pointer, ctx *Ctx) error`. Methods on nested composition types
  get their own descriptors' action tables.
- `via.OnClick(m)` / `via.OnChange(m)` / `via.OnKeyDown(key, m)` /
  `via.OnSubmit(m)` take `func(*via.Ctx) error` as a method value.
  Implementation: `runtime.FuncForPC(reflect.ValueOf(m).Pointer()).Name()` →
  strip to method name → look up `actionByID[name]` on the owning descriptor.
  Panics at render time if the method wasn't registered (unexported, wrong
  signature).
- Testing calls the method directly:
  `c := &Counter{}; ctx := via.NewTestCtx(t, c); require.NoError(t, c.Inc(ctx));
  assert.Equal(t, 1, c.Hits.Get())`.
- Wire path: `POST /_action/{cmpID}/{methodName}`. One map lookup per request
  for dispatch (acceptable — network-bounded).

### Per-request arena (`*requestState`)

```go
type requestState struct {
    descriptor *cmpDescriptor
    cmp        unsafe.Pointer   // *C bound for this request
    signals    []signalCell    // len = len(descriptor.signalSlots) across tree
    states     []stateCell       // tab-scoped; user/app go through registries
    params     []string        // len = len(descriptor.paramSlots)
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

- `rsPool.Get()` returns a pre-sized `*requestState`; `Reset()` clears dirty +
  rewinds buffers.
- `Ctx` becomes a thin wrapper over `*requestState` (keeps the existing name
  so most `h` helpers are unchanged).

### Pools

- `renderBufPool` — `sync.Pool[*bytes.Buffer]` with 8KB starting capacity.
  Used by `flushPatches`, `SyncElements`, SSE frame writes.
- `ssePool` — `sync.Pool[*sseWriter]` with a 4KB scratch; frame prefixes are
  constant byte slices (no `fmt.Sprintf`).
- `cmpPool` per descriptor (pooled root composition `*C`).
- `rsPool` per descriptor (sized by signal/state/param counts).

### Middleware & route params

- Descriptor stores a flat `mwChain` built once at registration from app +
  group + page middleware. No per-request `append`.
- Path params decoded into struct fields by descriptor `paramSlots`: `strconv`
  for int/uint/float/bool, direct assignment for string. No per-request
  `map[string]string`.

### Plugins

- `via.Plugin` interface unchanged: `Register(*App)`. `Plugin()` constructor
  convention unchanged.
- Existing plugins (`picocss`, `echarts`) port to the new API: app-level state
  uses `via.AppState[T]`; plugin-mounted pages use `via.Mount[P](app, ...)`.

### Testing

- `via.NewTestCtx(t testing.TB, p Page, opts ...TestOption) *Ctx` — instantiates
  the page through the same descriptor path, returns a bound `*Ctx`. Supports
  `WithPathParams(...)`, `WithSession(...)`.
- `testclient` drives actions by name: `tc.Action("Inc").Fire()` /
  `tc.Signal("step").Set(3)` — no regex extraction. Internally uses the
  descriptor's `actionByID` / `signalByID` tables exposed through an internal
  test hook.

### Exported surface: organized by concern (sub-packages)

A single flat `via.*` namespace crowds fast. The new surface splits along
themes so every call-site reads like prose. Each sub-package is small, focused,
and the import line pays for itself by giving the dev short names at the
point of use:

```go
import (
    "github.com/go-via/via"
    "github.com/go-via/via/on"
    "github.com/go-via/via/scope"
    "github.com/go-via/via/h"
)
```

**`via` (root) — the things every composition file touches**

- Types: `Composition` (interface), `Ctx`, `Signal[T]`, `State[T]` (tab-scoped —
  the common case), `App`, `Plugin`.
- Factories: `New(opts ...Option) *App`, `Mount[C any](app *App, route string)`,
  `MountOn[C any](g *Group, route string)`.
- App options: `WithPlugins(...)`, plus existing app-level helpers.
- App methods retained: `Use`, `Group`, `AppendToHead`, `AppendToFoot`,
  `AppendAttrToHTML`, `HandleFunc`, `Start`, `Shutdown`.

**`via/on` — event bindings**

Short verbs at the call-site; `on.Click(c.Inc)` reads like HTML:

- `on.Click(m, opts ...TriggerOption) h.H`
- `on.Change(m, opts ...TriggerOption) h.H`
- `on.Input(m, opts ...TriggerOption) h.H`
- `on.Submit(m, opts ...TriggerOption) h.H`
- `on.Key(key string, m, opts ...TriggerOption) h.H`
- `on.SetSignal[T any](sig Signal[T], v T) TriggerOption` — trigger option for
  bundled signal writes.

**`via/scope` — non-tab state scopes**

Tab-scoped state is the default (lives at `via.State[T]`). Wider scopes are
explicit:

- `scope.User[T any]` — session-scoped state, survives across tabs in one session.
- `scope.App[T any]` — app-scoped state, shared across all sessions.

`c.Hits.Set(...)` is already scope-typed — swapping `via.State[int]` for
`scope.User[int]` is a one-token change and a compile-time scope check.

**`via/test` — testing helpers**

- `test.NewCtx(t testing.TB, c via.Composition, opts ...CtxOption) *via.Ctx` —
  direct-method testing.
- `test.Client(t testing.TB, app *via.App, opts ...ClientOption) *Client` —
  replaces the ad-hoc `testclient` with a name-addressed client:
  `tc.Action("Inc").Fire()`, `tc.Signal("step").Set(3)`.
- `test.WithPathParams(...)`, `test.WithSession(...)` — option helpers.

**`h` — HTML builder (unchanged)**

Stays as-is. `Signal[T]` gains `.Bind()`, `.Text()`, `.Show()` methods that
return `h.H` so the builder and the reactive primitives compose without a third
package.

### Plugin constructors (unchanged convention)

Every plugin package exposes `Plugin()` as its public constructor (per
CONVENTIONS.md). Usage stays `via.New(via.WithPlugins(picocss.Plugin(),
echarts.Plugin()))`.

### Read like a Go dev who's never seen it

```go
import (
    "github.com/go-via/via"
    "github.com/go-via/via/on"
    "github.com/go-via/via/h"
)

type Counter struct {
    Hits via.State[int]
    Step via.Signal[int] `via:"step,init=1"`
}

func (c *Counter) Inc(ctx *via.Ctx) error {
    c.Hits.Set(ctx, c.Hits.Get(ctx)+c.Step.Get(ctx))
    return nil
}

func (c *Counter) View(ctx *via.Ctx) h.H {
    return h.Div(
        h.P(h.Text("Count: "), c.Hits.Text()),
        h.Input(h.Type("number"), c.Step.Bind()),
        h.Button(h.Text("+"), on.Click(c.Inc)),
    )
}
```

### Removed

- `Cmp` and all its methods (`View`, `Action`, `Init`, `Dispose`, `Component`,
  `Content`).
- `App.Page(route, func(*Cmp))`.
- Functional `Signal(cmp, init)` / `State(cmp, init, opts...)` /
  `AppSignal(v, ...)`.
- `runtime.Callers`-based state ID deduplication.
- `genRandID` for signals/actions.
- `ctx.GetPathParam` (replaced by struct-tag decode).
- Options like `WithScopeApp`, `WithScopeUser` (scope is now the handle type
  in `via/scope`).
- Root-level `via.OnClick`/`OnChange`/`OnInput`/`OnKeyDown`/`OnSubmit`/
  `ActionWithSetSignal` (moved under `via/on`).
- Root-level `via.UserState[T]`/`via.AppState[T]` (moved under `via/scope` as
  `scope.User[T]`/`scope.App[T]`).
- Root-level `via.NewTestCtx` (moved under `via/test`).

## Critical files

**via package files** (root package `via/`):

- `composition.go` — `Composition` interface, `cmpDescriptor`, reflection walk,
  pool setup, child-slot rendering, field-path qualified ids.
- `signal.go` — typed `Signal[T]` handle + cell storage.
- `state.go` — typed `State[T]` (tab only). User/App scopes live in `scope`.
- `action.go` — action-slot dispatch + `TriggerOption` type used by `on`.
- `runtime.go` — `requestState`, pools, bitset patch queue, descriptor-driven mount.
- `app.go` — registry of descriptors, numeric `id` context registry, pool wiring.
- `group.go` — `MountOn[C]` + middleware flatten.
- `middleware.go` — no per-request copy; descriptor holds flat chain.
- `config.go` — drop scope options.

**New sub-packages within via/:**

- `on/on.go` — `Click`, `Change`, `Input`, `Submit`, `Key`, `SetSignal`.
- `scope/scope.go` — `User[T]` and `App[T]` scoped state handles.
- `test/test.go` — `NewCtx`, `Client`, `WithPathParams`, `WithSession`.

**New internal files:**

- `arena.go` — `requestState` + `sync.Pool` lifecycle.
- `bench_test.go` — `-benchmem` ceilings gated in `ci-check.sh`.
- `internal/shared/` — shared cell/encoder primitives used by both state
  and scope.

**Tests written from scratch:**

- All tests written new against the new API; `internal/viaold` used as
  behavioral reference.

**Touched but not restructured:**

- `h/` — unchanged surface; `Signal.Text()` / `Signal.Bind()` callers.
- `plugins/picocss/` and `plugins/echarts/` — port to new typed API.
- `sess.go` — keep; `scope.User[T]` builds on it.

## Implementation order (test-first, per CONVENTIONS.md)

Each step: write failing test against the public API → implement → green → commit.

1. Create `via/` package from scratch (reference `internal/viaold` for
   behavioral equivalence).
2. `Composition` interface + `Mount[C]` happy path with a struct that has
   only `View`. Descriptor build + pooled `*C` per request. Test:
   `TestMount_rendersComposition`.
3. `path:"x"` tag decoding for `int`, `string`. Test:
   `TestMount_decodesPathParams`.
4. `Signal[T]` typed read/write with field-offset storage; scalar encoder.
   Test: `TestSignal_typedGetSet`, `TestSignal_initFromTag`.
5. Action method discovery + `via.OnClick(method)`. Test:
   `TestAction_firesMethodByName`.
6. `State[T]` tab-scoped + reactive re-render on `Set`. Test:
   `TestState_reRendersOnSet`.
7. `UserState[T]`, `AppState[T]`. Test: scope coherence across tabs/sessions.
8. Nested compositions as fields; pass-through signal handles. Test:
   `TestComposition_childInheritsParentSignal`.
9. Per-request `sync.Pool` arena + bitset patch queue. Test:
   `TestRequestState_resetsBetweenRequests`; bench:
   `BenchmarkCounterAction_zeroAllocs`.
10. Buffer pools on every write path. Bench: `BenchmarkCounterRender`.
11. Composite-typed signal with cached encoder. Bench:
    `BenchmarkCompositeSignal_encodesOncePerChange`.
12. Lifecycle: `Init`/`Dispose` detection + tab disposal hooks. Test:
    `TestComposition_disposeRunsOnTabClose`.
13. Port `picocss` and `echarts` plugins. Integration test: existing plugin
    behavioral tests pass against new API.
14. New examples in `internal/examples/**` (counter, counter-comp, etc.) —
    verify behavior matches `internal/viaold/examples/**`.
15. Replace `testclient` surface with name-addressed helpers. Migrate all
    `*_test.go` files to new addressing.
16. Delete dead code: old `Cmp` runtime handle, `App.Page(func(*Cmp))`,
    functional `Signal/State` constructors, `runtime.Callers` ID logic,
    `genRandID`. Confirm `go vet ./...` + `go test -race ./...` stay green.
17. Add `-benchmem` thresholds to `ci-check.sh`. Any regression past target
    fails CI.

## Verification

- `go test -race ./...` — full suite green; all new tests written from scratch
  (test names follow the `TestSubject_behavior` convention).
- Behavioral equivalence: new tests match `internal/viaold` behavior, not ported
  from it.
- `go test -bench=. -benchmem ./...` — `BenchmarkCounterAction_zeroAllocs` asserts
  **0 allocs/op** for a scalar-signal bump in steady state; `BenchmarkCounterRender`
  asserts bounded allocs/op (buffer rental only).
- Manual smoke: run `internal/examples/counter` and `internal/examples/countercomp`,
  open two browser tabs, confirm tab-scoped and app-scoped state behave per the
  expected differentiation.
- Plugin smoke: run an example that uses `picocss` and `echarts`; confirm
  theme switching + chart updates work end-to-end.
- `ci-check.sh` exits non-zero on any alloc regression past configured thresholds.
