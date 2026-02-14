# vtest - Ergonomic Testing for Via Apps

Package vtest provides ergonomic testing utilities for Via applications with a stateful Page API.

## Features

- **Stateful page model** - Pages maintain session and SSE state
- **Cookie jar** - Session cookies persist across requests
- **Action triggering** - Click buttons and trigger actions
- **SSE support** - Real-time updates captured in tests
- **Session scopes** - Test tab, session, and app-scoped state

## Stateful Page API (Recommended)

```go
import (
    "testing"
    "github.com/go-via/via/vtest"
)

func TestMyApp(t *testing.T) {
    v := myapp.New()
    vtest.SetHandler(v.HTTPServeMux())

    page := vtest.Visit("/")
    defer page.Close()

    // Assert initial state
    page.AssertText(t, "Welcome")
    page.AssertText(t, "Count: 0")

    // Click button by text
    page.Click("+")
    page.AssertText(t, "Count: 1")

    // Actions automatically update page state via SSE
    page.Click("+")
    page.AssertText(t, "Count: 2")
}
```

## Example: Counter App

```go
func TestCounter(t *testing.T) {
    v := counter.NewCounterPage()
    vtest.SetHandler(v.HTTPServeMux())

    page := vtest.Visit("/")
    defer page.Close()

    page.AssertText(t, "Count: 0")

    page.Click("+")
    page.AssertText(t, "Count: 1")

    page.Click("-")
    page.AssertText(t, "Count: 0")
}
```

## API

### Global

- `SetHandler(handler http.Handler)` - Set default handler for Visit
- `Visit(path string) *Page` - Create stateful page by visiting path

### Page

- `Click(buttonText string)` - Click button by text content
- `AssertText(t, text string)` - Assert page contains text
- `Close()` - Close page and SSE connection

### Low-Level API (for advanced use)

- `New(handler http.Handler) *Tester` - Create tester
- `Get(path string) *Response` - Perform GET request
- `TriggerAction(t, index int)` - Trigger action by index
- `SSE(sessionID string) *SSE` - Establish SSE connection

## Test Apps

vtest includes minimal Via apps for testing:

- `NewCounterApp()` - Counter with increment/decrement
- `NewTodoApp()` - Todo list with add/clear
- `NewGreeterApp()` - Greeter with name state

These are used internally for testing vtest and available for your tests.

## Testing Session Scopes

The Page API properly simulates browser cookie behavior, enabling tests for:

```go
// Test tab scope isolation
page1 := VisitWith(handler, "/")
page2 := VisitWith(handler, "/")
page1.Click("Increment")
page1.AssertText(t, "Count: 1")
page2.AssertText(t, "Count: 0") // Tab scope = different state

// Test app scope sharing
page1.Click("Inc Global")
page1.AssertText(t, "Total: 1")
page2.AssertText(t, "Total: 1") // App scope = shared state

// Test authentication
page := VisitWith(handler, "/")
page.AssertText(t, "Not logged in")
page.Click("Login")
page.AssertText(t, "User: Alice")
page.Click("Logout")
page.AssertText(t, "Not logged in")
```

See `scopes_test.go` for complete examples.
