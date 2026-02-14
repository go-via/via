# AGENTS.md

Guidelines for AI agents working on the Via codebase.

## What is Via?

Via is a reactive real-time web framework for Go that eliminates JavaScript
through Server-Sent Events (SSE) and the Datastar library. The mental model:
**Go on the server — HTML in the browser — real-time updates via SSE.**

## Build & Test Commands

```bash
# Run all CI checks (format, vet, build, tests)
./ci-check.sh

# Individual commands
go fmt ./...
go vet ./...
go build ./...
go test ./...

# Run a specific test
go test -run TestPageRoute ./...
```

Always run `./ci-check.sh` before committing changes.

## Code Style

### Go Style: Flat Code

Early returns over nested conditionals. Guard clauses at the top.

```go
// Yes
func process(x *Thing) error {
    if x == nil {
        return errNilThing
    }
    if !x.Valid() {
        return errInvalid
    }
    return x.Do()
}
```

### Imports

Group: stdlib, third-party, internal. Use `go fmt` to organize.

### Naming

- **Packages**: lowercase, single word (`h` for HTML DSL)
- **Exported types**: PascalCase (`StateHandle`, `ActionHandle`)
- **Unexported types**: camelCase (`signal`, `actionIDCounter`)
- **Constants**: MixedCaps (`LogLevelDebug`)
- **Interfaces**: nouns ending in "er" (`H` for HTML nodes)
- **Tests**: `TestFunction_BehaviorDescription`

### Types & Generics

- Use generics for type-safe state: `State[T any](initial T) *StateHandle[T]`
- Keep generic constraints minimal
- Return concrete types from constructors

### Error Handling

- Guard clauses at top, return early
- Avoid error wrapping unless necessary
- `panic()` only for programmer errors (nil view function)

### Performance: Two Zones

**Hot paths / internals:** Pass structs by pointer, pre-allocate slices
`make([]T, 0, n)`, avoid closures that capture variables.

**Public API:** Method chaining, fluent interfaces, allocations acceptable
for cleaner API.

### TDD/ATDD

Red-Green-Refactor: write failing test, write minimum code to pass,
refactor only when green. Test names describe behavior:
`TestState_GetReturnsInitialValue`. Mock I/O at boundaries.

## Architecture

### The Composition/Session Pattern

Via separates **composition-time** (defining the page) from **runtime**
(handling requests):

```go
v.Page("/counter/{id}", func(c *via.Composition) { // Composition = page definition
    count := via.State(0)                          // Server-side state
    step := via.Signal(c, 1)                       // Client-side signal

    increment := via.Action(c, func(ctx *via.Context) { // Session = runtime context
        count.Set(s, count.Get(s) + step.Get(s))
    })

    c.View(func(ctx *via.Context) h.H {              // View = read-only by convention
        return h.Div(
            h.P(h.Textf("Count: %d", count.Get(s))),
            h.Input(h.Type("number"), h.Name("step"), step.Bind()),
            h.Button(h.Text("+"), increment.OnClick()),
        )
    })
})
```

**Context safety:** `State.Set()` warns if called during view render.
Actions run in action mode, views in view mode. Mutations in views are
ignored with warnings.

### Complete Counter Example

Full working example showing idiomatic Via patterns:

```go
package main

import (
    "log"
    "net/http"

    "github.com/via/via"
    "github.com/via/via/h"
)

func main() {
    v := via.New()

    // Counter page with path parameter
    v.Page("/counter/{id}", func(c *via.Composition) {
        // State declared at composition-time
        count := via.State(0)

        // Actions have Session (read + write access)
        increment := via.Action(c, func(ctx *via.Context) {
            count.Set(s, count.Get(s) + 1)
        })

        decrement := via.Action(c, func(ctx *via.Context) {
            count.Set(s, count.Get(s) - 1)
        })

        reset := via.Action(c, func(ctx *via.Context) {
            count.Set(s, 0)
        })

        // View has Session (read access by convention)
        c.View(func(ctx *via.Context) h.H {
            id := s.PathParam("id")

            return h.Div(
                h.H1(h.Textf("Counter #%s", id)),
                h.Div(
                    h.P(h.Textf("Count: %d", count.Get(s))),
                    h.Button(h.Text("-"), decrement.OnClick()),
                    h.Button(h.Text("+"), increment.OnClick()),
                    h.Button(h.Text("Reset"), reset.OnClick()),
                ),
            )
        })
    })

    v.Start()
}
```

**Key patterns demonstrated:**

- State created at composition-time, accessed at runtime
- Actions modify state via `State.Set(s, value)`
- Views read state via `State.Get(s)` (mutations warned)
- Path parameters accessed via `s.PathParam("name")` on Session
- `OnClick()` attaches action to button click events
- Real-time updates happen automatically via SSE

### Key Types

| Type                | Purpose                                        |
| ------------------- | ---------------------------------------------- |
| **V**               | Root application, manages routing and sessions |
| **Composition**     | Page composition (`View()`, `Action()`)        |
| **Context**         | Runtime context for views and actions          |
| **StateHandle[T]**  | Server-side reactive state                     |
| **SignalHandle[T]** | Client-side reactive values (browser state)    |
| **h.H**             | HTML node interface                            |

### File Organization

| File             | Purpose                                                    |
| ---------------- | ---------------------------------------------------------- |
| `via.go`         | Root `V` type, routing, SSE infrastructure                 |
| `composition.go` | `Composition` struct, `Action()`, `View()`, `Signal()`     |
| `component.go`   | `ComposeFn`, `CompHandle`, `c.Component()` for reusability |
| `state.go`       | `StateHandle[T]` generic server-side reactive state        |
| `signal.go`      | `SignalHandle[T]` generic client-side reactive signals     |
| `action.go`      | `ActionHandle` and event handlers (OnClick, OnChange, etc) |
| `session.go`     | Session management with state and signal stores            |
| `h/`             | HTML DSL wrapping gomponents                               |

### State vs Signal

- **`State[T]`** (`state.go`): Server-side reactive state, synced via SSE patches.
- **`SignalHandle[T]`** (`signal_handle.go`): Client-side reactive
  values (browser state), sent with actions.

### HTML DSL (h package)

All elements return `h.H`: `h.Div(...)`, `h.P(...)`, `h.Button(...)`,
`h.Text()`, `h.Data()` for Datastar attrs, `h.HTML5()` for template.

### Components

Components are self-contained, reusable UI elements that compose exactly like
pages. They use the same `c.View()`, `via.State()`, `via.Signal()`, and
`via.Action()` API.

**Key types:**

- `ComposeFn` - Function signature for component composition: `func(c *Composition)`
- `*CompHandle` - Handle returned by `c.Component()` with `Mount(s *Context) h.H` method

**Basic component:**

```go
func makeCounter(label string) via.ComposeFn {
    return func(c *via.Composition) {
        count := via.State(0)
        increment := via.Action(c, func(ctx *via.Context) {
            count.Set(s, count.Get(s)+1)
        })
        c.View(func(ctx *via.Context) h.H {
            return h.Div(
                h.P(h.Textf("%s: %d", label, count.Get(s))),
                h.Button(h.Text("+"), increment.OnClick()),
            )
        })
    }
}

// Usage in page
v.Page("/", func(c *via.Composition) {
    counter := c.Component(makeCounter("Hits"))
    c.View(func(ctx *via.Context) h.H {
        return h.Div(counter.Mount(s))
    })
})
```

**How components work:**

1. `c.Component(composeFn)` creates a child `Composition` with unique ID
2. Calls `composeFn(child)` to configure component
3. Merges child's actions and signals into parent (bubble up)
4. Returns `*CompHandle{id, viewFn}`
5. `handle.Mount(s)` wraps component in `<div id="...">` (not `<main>`)

**Props via closure:**

```go
type CounterProps struct {
    Name  string
    Step  int
}

func NewCounter(props CounterProps) via.ComposeFn {
    return func(c *via.Composition) {
        count := via.State(0)
        step := via.Signal(c, props.Step)
        increment := via.Action(c, func(ctx *via.Context) {
            count.Set(s, count.Get(s)+step.Get(s))
        })
        c.View(func(ctx *via.Context) h.H {
            return h.Div(
                h.H2(h.Text(props.Name)),
                h.P(h.Textf("Count: %d", count.Get(s))),
                h.Button(h.Text("+"), increment.OnClick()),
            )
        })
    }
}

// Usage
counter1 := c.Component(NewCounter(CounterProps{Name: "A", Step: 1}))
counter2 := c.Component(NewCounter(CounterProps{Name: "B", Step: 10}))
```

**Nesting:**

Components can nest other components via `c.Component()` inside `ComposeFn`.
Actions and signals bubble up through the merge chain.

```go
makePanel := func(c *via.Composition) {
    counterA := c.Component(makeCounter("Counter A"))
    counterB := c.Component(makeCounter("Counter B"))
    c.View(func(ctx *via.Context) h.H {
        return h.Div(
            h.H2(h.Text("Panel")),
            counterA.Mount(s),
            counterB.Mount(s),
        )
    })
}

panel := c.Component(makePanel)
```

**Key differences from pages:**

- Pages wrap in `<main id="...">`, components wrapped by `Mount()` in `<div id="...">`
- Component `View()` stores raw viewFn, page `View()` wraps in `<main>`
- Components share parent's SSE patchChan
- Multiple instances have independent state (separate IDs)

## Common Tasks

**Adding HTML DSL elements:** Add to `h/elements.go`, returns `h.H` interface.

**Adding server-side state:** `State(initial)` in composition. Read with
`state.Get(s)`, write with `state.Set(s, value)` (in actions).

**Adding client-side signals:** `via.Signal(c, initial)` in composition. Read with
`signal.Get(s)`, write with `signal.Set(s, value)` (in actions). Use `signal.Bind()`
for input binding and `signal.Text()` for reactive display.

**Creating action handlers:** `via.Action(c, func(ctx *via.Context) { ... })`, attach with
`trigger.OnClick()`, access params with `s.PathParam("name")`.

**Testing:** Use `httptest` to exercise full HTTP stack. See existing tests
in `via_test.go` for patterns.

## Design Principles

1. **Flat code** — Early returns, guard clauses, minimal nesting
2. **Type safety** — Use generics to prevent errors at compile time
3. **Zero JS** — Write Go, get real-time web apps
4. **Two performance zones** — Hot paths: zero-allocation; API: ergonomics

