# IMPL_SPEC — Slice 1: the stateless signal-counter

This pins the concrete Go contract for the FIRST vertical slice. Read alongside
`DESIGN.md` (the why). Scope of slice 1 is deliberately narrow: **stateless,
request/response, no SSE / islands / Stream / State[T] / Local[T] yet.** Just
enough to make a `Signal`-backed counter render and increment, fully testable
with `net/http/httptest`.

Module `github.com/go-via/via`, Go 1.24, **stdlib only**. The Datastar client is
the vendored `/datastar.js` at repo root — embed it with `//go:embed`.

## Packages

- `github.com/go-via/via/h` — the HTML DSL + renderer.
- `github.com/go-via/via` — Ctx, Signal, OnClick, Register.
- `example/counter` — the demo + tests.

## Package `h`

`H` is the single tree type, an interface:

```go
type H interface{ render(*Renderer) }   // unexported method ⇒ sealed to this module
```

Two flavours implement it: **nodes** (rendered in element body) and
**attributes** (rendered inside the opening tag). Distinguish with a marker:

```go
type Attr interface { H; isAttr() }
```

`Renderer` accumulates bytes and assigns positional slot/action ids:

```go
type Renderer struct {
    buf   *bytes.Buffer
    ctx   Binder   // injected by the via package; see "Binder" below
}
func (r *Renderer) WriteString(s string)   // raw, caller pre-escaped
func (r *Renderer) WriteEscaped(s string)   // HTML-escapes text/attr values
```

`Binder` is the bridge `h` needs into `via` without importing it (avoid import
cycle). Define it in `h`:

```go
// Binder lets dynamic slots (handles, events) claim positional ids and read
// hydrated values during a render pass. The via package supplies the impl.
type Binder interface {
    SignalSlot() string                 // returns next "s0","s1",… and advances
    SignalInit(slot string) (any, bool)  // hydrated value for slot, if present
    ActionSlot(fn func()) string         // registers a handler, returns "0","1",…
}
```
(Handles/events live in package `via`; they receive `*Renderer`, read
`r.ctx`.)

### Element constructors

```go
func El(tag string, kids ...H) H        // generic element
func Div(kids ...H) H
func Span(kids ...H) H
func H1(kids ...H) H
func Button(kids ...H) H
func Input(kids ...H) H
func Body(kids ...H) H
// …enough for the counter; add as needed.
```

Rendering an element: `<tag` + all `Attr` kids (in order) + `>` + all non-attr
kids (in order) + `</tag>`. Void elements (`input`) self-close, no children
body. Partition kids into attrs/nodes by the `Attr` marker.

### Text

```go
type Stringish interface{ ~string | ~int | ~int8 | ~int16 | ~int32 | ~int64 |
    ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64 }

func Str[T Stringish](v T) H   // escaped static text node
```
Render: `fmt`-format the value, HTML-escape, write. NO `any`.

### Raw attribute helper (internal use by via)

```go
func Data(name, val string) Attr   // data-<name>="val" (val escaped)
func RawAttr(name, val string) Attr
```

### Static-skeleton note

Slice 1 may use a straightforward per-render tree walk (build tree → render
once). Do NOT prematurely cache skeletons. Leave a `// TODO(static-skeleton)`
where caching would later slot in. Correctness + the API shape matter now; the
perf optimization is a later slice.

## Package `via`

### Ctx

Per-request. Implements `h.Binder`.

```go
type Ctx struct {
    inSignals map[string]json.RawMessage // hydrated from the request
    nextSig   int
    actions   []func()                   // positional action table
    dirty     map[string]any             // signals changed this request
}
func (c *Ctx) SignalSlot() string                 // "s"+nextSig++, also remembers order
func (c *Ctx) SignalInit(slot string) (any, bool)
func (c *Ctx) ActionSlot(fn func()) string         // len(actions); append; return index
func (c *Ctx) markDirty(slot string, v any)
```

### Signal[T]

A composition field. Client-resident; round-trips per request; renders as a
Datastar text-bound span. `T` constrained to something JSON-encodable; for slice
1 support `int` and `string` (use `T any` with json marshal/unmarshal, document
that T must be JSON-round-trippable).

```go
type Signal[T any] struct {
    slot string // assigned on first render this request
    val  T
}

// render (h.H): claim a slot, hydrate from request, emit a reactive span.
func (s *Signal[T]) render(r *h.Renderer) {
    s.slot = r.Binder().SignalSlot()
    if raw, ok := r.Binder().SignalInit(s.slot); ok { s.val = decode[T](raw) }
    // emit: <span data-text="$s0">{escaped current val}</span>
    // AND ensure the signal is declared: see page-level data-signals below.
}

func (s *Signal[T]) Get() T { return s.val }
func (s *Signal[T]) Set(ctx *Ctx, v T)            // val = v; ctx.markDirty(slot, v)
func (s *Signal[T]) Add(ctx *Ctx, d T)            // only for numeric T; or provide on a numeric subtype
func (s *Signal[T]) Bind() h.Attr                  // data-bind="s0"  (needs slot assigned)
```

`*Signal[T]` is an `h.H` (pointer receiver `render`). In a View you write
`h.H1(&c.Count)` — wait: NO `&`. Because `c` is the `*Counter` via owns and
`Count` is an addressable field, but passing `c.Count` to `h.H1(...h.H)` needs
`c.Count` to BE an `h.H`. A value `Signal[T]` with a pointer `render` does not
satisfy `h.H`. RESOLUTION for slice 1: give `Signal[T]` a **value-receiver**
`render` that reads through an internal pointer is impossible without identity.
Instead expose a method:

```go
func (s *Signal[T]) Node() h.H   // returns an h.H bound to this signal (captures &s via addressable field through the method receiver)
```
and write `h.H1(c.Count.Node())`. `Node()` has a pointer receiver, so
`c.Count.Node()` auto-addresses — no `&`. The returned `h.H` closes over the
`*Signal[T]`. (This keeps the "no &" guarantee; `DESIGN.md`'s bare-handle form
is a later ergonomic pass once skeleton-caching lands.)

> Implementer: if you find a clean way to make the bare `h.H1(c.Count)` form
> work in slice 1 without `&` and without reflection, prefer it and note how.
> Otherwise use `.Node()` and leave a `// TODO(bare-handle)`.

`Add` requires numeric ops on `T`. For slice 1, implement `IntSignal = Signal[int]`
convenience OR constrain a separate `func (s *Signal[int]) Add`. Simplest:
provide `Set` generically and an `Add` only on a concrete `*Signal[int]` via a
free helper `via.Inc(ctx, &c.Count, 1)` — but that reintroduces `&`. Cleaner:
make `Add` a method on `*Signal[T]` constrained at the type level is impossible
(no per-method constraints pre-1.27). DECISION: for slice 1 the counter's
`Count` is `via.Num` (a concrete numeric signal type) with `Add`/`Set`:

```go
type Num struct{ Signal[int] }                 // embeds Signal[int]
func (n *Num) Add(ctx *Ctx, d int)             // n.Set(ctx, n.Get()+d)
```
Counter uses `Count via.Num`.

### OnClick

```go
func OnClick(fn func(*Ctx)) h.Attr
```
Returns an attribute that, at render, calls `r.Binder().ActionSlot(...)` to get
index `N`, wraps `fn` so dispatch can run it against the request Ctx, and emits
`data-on-click="@post('/_via/a/N')"`. The wrapped `func()` stored in the table
must, when invoked during dispatch, call `fn(ctx)` with the live ctx.

`c.Inc` is a method value `func(*Ctx)` — pointer-bound to the via-owned
instance, no `&`.

### Register

```go
func Register[T any](root T) http.Handler
```
`root` is taken by value; per request via copies it into an addressable local
`inst := root` and operates on `&inst` so pointer-receiver methods/handles work.

Routes (use `http.ServeMux` with Go 1.22 patterns):

- `GET /_via/datastar.js` → serve embedded `datastar.js`, `Content-Type:
  text/javascript`.
- `GET /{$}` (and `/`) → **render the page**:
  1. `inst := root`; make `ctx` with empty `inSignals`.
  2. Render `(&inst).View()` via a `Renderer` whose `Binder` is `ctx`. This
     assigns signal slots (s0…) and action indices, with zero-value signal
     values.
  3. Collect the initial signals seen this render → emit a root wrapper:
     `<div id="root" data-signals='{"s0":0,...}'>` + rendered body + `</div>`.
  4. Wrap in a full HTML document: `<!doctype html><html><head><meta charset>
     <script type="module" src="/_via/datastar.js"></script></head><body>` +
     the root div + `</body></html>`. `Content-Type: text/html`.
- `POST /_via/a/{n}` → **dispatch an action**:
  1. Read request body → JSON object of signals → `ctx.inSignals`.
  2. `inst := root`; render `(&inst).View()` with `ctx` (a *bind pass*: assigns
     slots, hydrates each Signal from `inSignals`, fills the action table).
     Discard the produced HTML.
  3. `n := {n}`; if out of range → 410 Gone. Else run `ctx.actions[n]()`.
     The action mutates a signal → `ctx.dirty`.
  4. Respond `Content-Type: application/json`, body = JSON of `ctx.dirty`
     (e.g. `{"s0":6}`). Datastar applies it as a signal patch.

> The bind-pass-then-dispatch order is the crux: rendering the View is what
> assigns slots, hydrates signals, and populates the action table — all without
> reflection. Keep render deterministic so slot/action ids are stable between
> the GET page and the POST dispatch.

### View contract

```go
type viewer interface{ View() h.H }
```
`Register`/dispatch assert `(&inst).(viewer)`. `View` is pure and ctx-free.

## Example: `example/counter`

```go
type Counter struct{ Count via.Num }
func (c *Counter) Inc(ctx *via.Ctx) { c.Count.Add(ctx, 1) }
func (c *Counter) Dec(ctx *via.Ctx) { c.Count.Add(ctx, -1) }
func (c *Counter) View() h.H {
    return h.Div(
        h.H1(c.Count.Node()),
        h.Button(via.OnClick(c.Dec), h.Str("−")),
        h.Button(via.OnClick(c.Inc), h.Str("+")),
    )
}
func main() {
    http.Handle("/", via.Register(Counter{}))
    http.ListenAndServe(":8080", nil)
}
```

## Tests (httptest, table-driven, in the right packages)

1. `h`: element renders attrs-in-tag + escaped children; `Str` escapes `<`,`&`.
2. `via`: GET / returns a document containing `data-signals` with `s0`, a
   `<span data-text="$s0">0</span>`, two buttons with `@post('/_via/a/0')` and
   `/_via/a/1`, and the embedded-datastar script tag.
3. `via`: POST `/_via/a/1` with body `{"s0":5}` returns
   `application/json` `{"s0":6}` (Inc). POST `/_via/a/0` with `{"s0":5}` →
   `{"s0":4}` (Dec).
4. `via`: POST to an out-of-range action index → 410.
5. `via`: GET `/_via/datastar.js` serves the embedded file with a JS content-type.

## Hard constraints (must hold in all public API)

- No `&` at any user call site (Register/Embed/View/actions/handles).
- No user-facing identifier strings, no reflection (`reflect`), no closures in
  the public API surface, no `any` in element/child signatures.
- `go vet ./...` clean; `env -u GOROOT /usr/bin/go build ./... && … test ./...`
  green.

Run Go as `env -u GOROOT /usr/bin/go <cmd>` (toolchain GOROOT workaround).
