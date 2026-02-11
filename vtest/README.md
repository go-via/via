# vtest - Ergonomic Testing for Via Apps

Package vtest provides ergonomic testing utilities for Via applications with a stateful Page API.

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
