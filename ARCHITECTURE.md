# Via Architecture

Reactive real-time web framework for Go. Server-side state, zero JavaScript.

## Mental Model

**Go on the server — HTML in the browser — real-time updates via SSE.**

Via eliminates the frontend/backend divide. You write Go that renders HTML.
State changes stream to the browser automatically via Server-Sent Events.
The Datastar library handles DOM updates client-side.

## Core Concepts

### Composition vs Runtime

Via separates **page definition** from **request handling**:

```go
// COMPOSITION TIME: Define the page structure
v.Page("/counter", func(c *via.Composition) {
    count := via.State(0)           // Declare server state
    
    increment := via.Action(c, func(ctx *via.Context) {
        count.Set(s, count.Get(s) + 1)  // RUNTIME: Mutate state
    })
    
    c.View(func(ctx *via.Context) h.H {   // RUNTIME: Read state
        return h.Div(
            h.Textf("Count: %d", count.Get(s)),
            h.Button(h.Text("+"), increment.OnClick()),
        )
    })
})
```

| Phase | Types Available | What You Do |
|-------|----------------|-------------|
| **Composition** | `Composition` | Declare state, signals, actions |
| **Context** | `Session` | Read/mutate state, access params |

### Type Safety

State and signals use Go generics for compile-time safety:

```go
count := via.State(0)           // *StateHandle[int]
name  := via.Signal(c, "")      // *SignalHandle[string]
```

## Architecture Components

### 1. V (Root Application)

File: `via.go`

The root type that manages:
- HTTP routing and handlers
- Session registry (per-tab state)
- SSE patch streaming infrastructure
- Server lifecycle (Start, Shutdown)

```go
v := via.New()
v.Page("/", handler)
v.Start()  // Blocks, serves HTTP
```

### 2. Composition

File: `composition.go`

Page/component definition context:

```go
type Composition struct {
    states   map[string]*stateHandle   // Server state
    signals  map[string]*signalHandle  // Client signals
    actions  map[string]*ActionHandle  // Event handlers
    viewFn   func(*Context) h.H        // Render function
}
```

Methods:
- `View(fn)` - Register render function
- `Action(fn)` - Register action handler
- `Signal(initial)` - Register client signal
- `Component(composeFn)` - Mount child component

### 3. Session

File: `session.go`

Per-tab runtime context:

```go
type Session struct {
    mode        sessionMode     // view | action
    store       *store          // State + signal values
    ss          *serverSession  // SSE channel, tab ID
    composition *Composition    // Page definition reference
}
```

Session modes enforce safety:
- **View mode**: Read-only. Mutations log warnings.
- **Action mode**: Read-write. Can mutate state and trigger sync.

### 4. StateHandle[T]

File: `state.go`

Server-side reactive state:

```go
type StateHandle[T any] struct {
    id      string
    initial T
}

func (s *StateHandle[T]) Get(sc *Context) T
func (s *StateHandle[T]) Set(sc *Context, value T)  // Auto-syncs
```

- Declared at composition-time
- Stored per-session in `store.state`
- `Set()` automatically triggers SSE patch
- Type-safe via generics

### 5. SignalHandle[T]

File: `signal.go`

Client-side reactive values:

```go
type SignalHandle[T SignalType] struct {
    id      string
    initial T
}

// Usage in HTML
h.Input(signal.Bind())      // Two-way binding
h.Span(signal.Text())       // Reactive text display
```

- Sent from browser with action requests
- Updated client-side via Datastar
- Constrained to: int*, uint*, float*, string, bool

### 6. ActionHandle

File: `action.go`

Event handlers executed server-side:

```go
type ActionHandle struct {
    id string
}

// Event binding
action.OnClick(opts...)
action.OnChange(opts...)
action.OnKeyDown(key, opts...)
action.OnInit()
```

Actions are HTTP GET endpoints at `/_action/{id}`.

### 7. UserHandle[T]

File: `user.go`

Session-scoped user authentication:

```go
type User struct {
    ID   string
    Name string
    Role string
}

// Create at composition time (module level)
var user = via.NewUserHandle[User]()

// In action - set user
user.SetUser(s, User{ID: "1", Name: "Alice", Role: "admin"})

// In action - check auth
if u, ok := user.Get(s); ok {
    // user is logged in
}

// In action - logout
user.Logout(s)  // clears user and invalidates session cookie
```

UserHandle:
- Always session-scoped (persists across tabs for same user)
- Generic type for any user struct
- Logout invalidates session for security
- Integrates with Via's session management

### 8. Components

File: `component.go`

Reusable UI elements:

```go
type ComposeFn func(c *via.Composition)
type CompHandle struct {
    id     string
    viewFn func(*Context) h.H
}

// Usage
counter := c.Component(makeCounter("Hits"))
counter.Mount(s)  // Returns h.H wrapped in <div id="...">
```

Components:
- Create child Composition with unique ID
- Bubble actions/signals up to parent
- Support nesting (components in components)
- Sync fragments (not full page) on action

### 8. SSE Infrastructure

Files: `via.go`, `session.go`

Real-time update mechanism:

```go
type patch struct {
    typ     patchType  // elements | signals | script
    content string
}
```

Flow:
1. `State.Set()` calls `Session.Sync()`
2. `Sync()` re-renders view
3. HTML diff sent as SSE patch
4. Datastar applies patch to DOM

### 9. HTML DSL

Directory: `h/`

Type-safe HTML generation wrapping gomponents:

```go
// Elements
h.Div(children...)
h.P(children...)
h.Button(children...)

// Attributes
h.ID("foo")
h.Class("bar")
h.Data("signal-foo", "value")

// Datastar helpers
h.DataInit("expression")
h.DataEffect("expression")
h.DataIgnoreMorph()
```

## Request Flow

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Browser   │────▶│  HTTP GET   │────▶│   V Router  │
│             │     │  /counter   │     │             │
└─────────────┘     └─────────────┘     └──────┬──────┘
                                               │
                                               ▼
                                        ┌─────────────┐
                                        │ Composition │
                                        │  (cached)   │
                                        └──────┬──────┘
                                               │
                                               ▼
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│  Datastar   │◀────│ SSE Stream  │◀────│   Session   │
│  (DOM)      │     │  (patches)  │     │  (runtime)  │
└─────────────┘     └─────────────┘     └─────────────┘
                                               ▲
                                               │
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Browser   │────▶│  HTTP GET   │────▶│   Action    │
│   Click     │     │ /_action/id │     │  Handler    │
└─────────────┘     └─────────────┘     └─────────────┘
```

1. **Page load**: Composition runs once, view renders, session created
2. **Action**: HTTP GET to action endpoint, session retrieved, handler runs
3. **Sync**: State mutation triggers re-render, patch streamed via SSE
4. **DOM update**: Datastar applies patch without page reload

## Session Lifecycle

```
┌─────────┐    ┌─────────────┐    ┌─────────────┐    ┌─────────┐
│  HTTP   │───▶│  Session    │───▶│  Session    │───▶│  Close  │
│ Request │    │  Created    │    │  Active     │    │ Beacon  │
└─────────┘    │  (tabID)    │    │  (patches)  │    │ /unload │
               └─────────────┘    └─────────────┘    └─────────┘
```

- **Creation**: First request generates 32-char hex tabID
- **Active**: SSE connection streams patches
- **Cleanup**: `beforeunload` beacon sends close request

## State Scopes

State can have different lifetimes using the `WithScope` option:

```go
// Tab scope (default) - unique per browser tab
count := via.State(c, 0)

// Session scope - shared across tabs for same user
token := via.State(c, "", via.WithScope(via.ScopeSession))

// App scope - global across all users
visits := via.State(c, 0, via.WithScope(via.ScopeApp))
```

| Scope | Lifetime | Use Case |
|-------|----------|----------|
| `ScopeTab` | Per browser tab | Form inputs, UI state |
| `ScopeSession` | Per user (cookie) | Auth tokens, preferences |
| `ScopeApp` | Global | Global counters, config |

### How It Works

- **Tab ID**: Generated per page load, stored in `via-c` signal
- **Context ID**: Cookie-based (`via_sid`), shared across tabs
- Actions receive both: `via-c` for tab state, cookie for session state
- State.Set() automatically uses the correct storage based on scope

## State vs Signal

| Aspect | State | Signal |
|--------|-------|--------|
| **Location** | Server | Client (browser) |
| **Sync** | SSE → browser | HTTP → server |
| **Mutation** | `state.Set(s, val)` | Automatic via binding |
| **Type** | `T any` | `T SignalType` (constrained) |
| **Persistence** | Session lifetime | Session lifetime |
| **Use case** | Server truth source | Form inputs, UI state |

## Component Model

Components compose exactly like pages:

```go
func NewCounter(name string) via.ComposeFn {
    return func(c *via.Composition) {
        count := via.State(0)
    increment := via.Action(c, func(ctx *via.Context) {
            count.Set(s, count.Get(s)+1)
        })
        c.View(func(ctx *via.Context) h.H {
            return h.Div(
                h.H2(h.Text(name)),
                h.P(h.Textf("Count: %d", count.Get(s))),
                h.Button(h.Text("+"), increment.OnClick()),
            )
        })
    }
}

// Usage
v.Page("/dashboard", func(c *via.Composition) {
    counterA := c.Component(NewCounter("A"))
    counterB := c.Component(NewCounter("B"))
    
    c.View(func(ctx *via.Context) h.H {
        return h.Div(
            counterA.Mount(s),
            counterB.Mount(s),
        )
    })
})
```

**Key behaviors:**
- Independent state per component instance
- Actions bubble up and sync only component fragment
- Nesting supported (components can contain components)
- Props via closure capture

## Design Decisions

### Why SSE over WebSockets?

- Server → client only (no bidirectional needed)
- Works over HTTP/1.1
- Automatic reconnection
- Simpler mental model

### Why generics for State?

- Compile-time type safety
- No reflection in hot paths
- IDE autocomplete works

### Why composition-time separation?

- Clear distinction between setup and runtime
- Composition can be cached
- Multiple sessions share one composition

### Why Datastar?

- Minimal JavaScript (~4KB)
- HTML-centric (no virtual DOM)
- Morphing algorithm for DOM updates
- Signals for client-side reactivity

## File Organization

| File | Purpose |
|------|---------|
| `via.go` | Root type, routing, SSE, session registry |
| `composition.go` | Composition struct, View(), action owners |
| `component.go` | ComposeFn, CompHandle, Component() |
| `state.go` | StateHandle[T] generic state |
| `signal.go` | SignalHandle[T] generic signals |
| `action.go` | ActionHandle, event bindings |
| `session.go` | Session management, Sync(), stores |
| `cfg.go` | Configuration, plugins, options |
| `h/*.go` | HTML DSL, elements, attributes |
| `vtest/*.go` | Testing utilities |
| `plugins/picocss/*.go` | Pico CSS theme plugin |

## Dependencies

- `maragu.dev/gomponents` - HTML generation
- `github.com/starfederation/datastar-go` - Datastar SDK
- `github.com/stretchr/testify` - Testing

## Plugins

Via supports plugins to extend functionality. Example: Pico CSS theme plugin.

```go
import "github.com/go-via/via/plugins/picocss"

// Initialize plugin to fetch and serve theme CSS
plugin := picocss.New(picocss.Options{
    Themes:       []string{"azure", "purple", "amber"},
    DefaultTheme: "azure",
})
plugin.Register(v)

// In page composition, add theme support
v.Page("/", func(c *via.Composition) {
    theme := picocss.Theme(c, picocss.Options{
        Themes: []string{"azure", "purple"},
    })
    
    c.View(func(ctx *via.Context) h.H {
        return h.Div(
            theme.Link(),  // <link> with Datastar binding
            theme.Buttons(),  // Theme switcher buttons
        )
    })
})
```

The plugin:
1. Fetches themes from Pico CDN and caches in memory
2. Serves theme CSS via `/_pico/theme/{name}` endpoint
3. Provides `Theme()` helper for page composition
4. Binds theme signal to `<link href>` for runtime switching

## Known Issues & Pain Points

### Fixed

**1. Signal FormatInitial() Bug** ✓

~~`signal.go` had incorrect type assertion using `v.(int)` for all int types.~~

Fixed: Separated type switch cases to use proper typed variables.

**2. Silent Patch Drops** ✓

~~Patches sent non-blocking with buffered channel (size 10).~~

Fixed: 
- Increased buffer size from 10 to 64
- Added warning logs when patches are dropped

### High Priority

**3. Session Memory Leaks** ✓

~~Sessions only clean up via explicit browser beacon.~~

Fixed:
- Added `lastAccess` timestamp to session struct
- Added `SessionTTL` option (default 30 min)
- Added `touch()` method updated on SSE connect
- Background goroutine cleans stale sessions every TTL/10
- Cleanup now also removes invalidated sessions (logout) after cookie MaxAge

### Medium Priority

**4. Runtime-Only View Safety** ✓

~~Views can accidentally mutate state.~~

Mitigated: All mutation methods now have view mode guards with tests:
- `State.Set()` - warns and ignores
- `Signal.Set()` - warns and ignores  
- `Session.Sync()` - warns and no-ops
- `Session.SyncFragment()` - warns and no-ops

Tests: `TestSession_*InViewModeWarns`, `TestSignal_SetInViewModeWarns`

Note: Compile-time safety would require API changes. Runtime warnings with test coverage is the current approach.

**5. Signal Type Conversion Complexity** ✓

~~155 lines of conversion logic. Can panic at runtime if types don't match.~~

Fixed:
- Line 52 panic fixed: now returns initial value instead of panicking
- Added comprehensive conversion tests for all types (float64 and string inputs)
- Tests: `TestSignal_GetInvalidTypeReturnsInitial`, `TestSignal_GetFloat64Conversion`, `TestSignal_GetStringConversion`, `TestSignal_GetInvalidStringReturnsInitial`

Note: Conversion logic is still complex (necessary for type safety), but now gracefully handles invalid types.

**6. Flaky Tests** ✓

~~`vtest/` uses `time.Sleep()` for SSE synchronization.~~

Fixed:
- Replaced fixed `time.Sleep()` calls with event-driven synchronization
- `notifyingResponseWriter` signals when SSE connection writes first data
- `WaitForEvent()` polls for actual SSE events instead of blind waiting
- Tests are now faster and more reliable

### Low Priority

**7. No Middleware Chain** ✓

~~Cross-cutting concerns require manual handler wrapping.~~

Implemented:
- Added `Middleware` type: `func(http.Handler) http.Handler`
- Added `Use(middleware ...Middleware)` method on `V`
- Middleware applies to all routes (pages, actions, SSE, internal)
- Execution order: first registered runs first (before handler)

```go
v.Use(middleware.Logger(), middleware.Recovery())

func middleware.Logger() func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // before
            next.ServeHTTP(w, r)
            // after
        })
    }
}
```

**Phase 2: Route Groups**

- Added `Group` struct with prefix and middleware
- Added `v.Group(prefix, fn)` method for creating route groups
- Added `g.Use(middleware...)` for group-specific middleware
- Added `g.Group()` for nested groups
- Added `g.Page()` for registering pages in groups
- Middleware execution: global → group (outer → inner, closer to handler)

**Phase 3: Action Middleware**

- Global middleware applies to all routes including actions (`/_action/*`)
- Group middleware applies to actions within that group
- Per-action middleware (not implemented - requires more invasive API changes)

**8. Registration Order Not Enforced** ✓

~~State/Signal must be registered before `View()` is called.~~

Fixed:
- Added `viewCalled` flag to Composition
- `State()` and `Signal()` now panic if called after `View()`
- `Action()` can still be called after View (no restriction needed)

```go
// Now enforces correct order
c := &Composition{}
State(c, 42)   // OK - before View
Signal(c, "hi") // OK - before View
c.View(...)    // OK - marks as called

// This now panics:
c.View(...)
State(c, 100)  // PANIC: State() called after View()
```

Tests: `TestState_PanicsAfterView`, `TestSignal_PanicsAfterView`, `TestState_BeforeViewOK`, `TestSignal_BeforeViewOK`, `TestAction_AfterViewOK`

---

## Future Considerations

See [Issues](https://github.com/via/issues) for planned improvements and known limitations.

