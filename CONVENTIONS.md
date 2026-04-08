# Conventions

## Test Names

Reasoning: Consistent naming makes tests discoverable and clarifies what
each test verifies.

Rule: Use `Test` + PascalCase subject + underscore + camelCase behavior
(present tense verb). The underscore separates *what* from *does what*.

- ✅ `TestSignal_returnAsString`
- ✅ `TestPage_panicsOnNoView`
- ✅ `TestPlugin_servesGzipWhenAccepted`
- ❌ `TestSignal` (vague — what about it?)
- ❌ `Test_signal_return_as_string` (wrong casing)

The name should read as a behavioral claim, not a description of what the
test does internally.

## Test-First

Reasoning: Writing the test first forces you to define the contract before
the implementation, and ensures every behavior has a corresponding test.

Rule: No implementation before a failing test. The sequence is always:
write test → confirm it fails correctly → implement → confirm it passes.

## Test Scope: Outside-In Through the Public API

Reasoning: Tests coupled to internals break on refactors, not on
regressions. The public API is the contract — that's what matters.

Rule: All tests enter the system through exported symbols. Use
`package foo_test` (external test package) as the default. Only drop into
`package foo` (internal) when testing unexported behavior that cannot be
observed through the public API at all — and treat this as a last resort.

## Mocking Preference: Real > Stub > Mock

Reasoning: The closer a test is to production behavior, the more confidence
it provides. Mocks that verify call counts or argument lists test wiring,
not behavior, and break when implementation changes.

Order of preference:

1. **Real** — use the actual implementation. Prefer `httptest.NewServer`
   over a fake HTTP client. Prefer an in-memory implementation over a stub.
2. **Stub** — a minimal hand-rolled implementation of an interface that
   returns canned values. No behavior verification.
3. **Mock** — a generated or framework-managed double that asserts on calls.
   Use only at true external system boundaries (third-party APIs, network,
   filesystem) where real and stub are impractical.

Rule: Prefer real or stub implementations for interfaces you own. Use a
mock only at true system boundaries — where Go code meets something
outside its process (third-party APIs, network, filesystem).

## Test Behavior, Not Implementation

Reasoning: Tests that assert on internal state, call counts, or private
function behavior are specifications of how something works, not what it
does. They impede refactoring.

Rule:

- Assert on observable outputs and side effects (HTTP response body,
  status codes, returned values, errors).
- Do not assert on internal state, execution order, or private fields.
- Use `assert.Contains` over `assert.Equal` when testing large or generated
  output — exact equality is brittle when the shape can change without
  breaking the contract.

Examples:

- ✅ `assert.Contains(t, body, "Hello Via!")`
- ✅ `assert.Equal(t, http.StatusOK, resp.StatusCode)`
- ❌ `assert.Equal(t, 3, len(v.handlers))` (internal state)
- ❌ `mockDep.AssertCalled(t, "Write", ...)` (call verification on owned code)

## Table-Driven Tests

Reasoning: Repeated test structure with varied inputs is clearer as a
table. It separates the cases from the mechanics.

Rule: Use table-driven subtests for parameterized scenarios. Each case
needs a `name` field. Run subtests with `t.Run`.

```go
tests := []struct {
    name  string
    input string
    want  string
}{
    {"empty input", "", ""},
    {"single word", "hello", "hello"},
}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        t.Parallel()
        assert.Equal(t, tt.want, fn(tt.input))
    })
}
```

## Parallel Tests

Reasoning: Parallel execution surfaces data races and speeds up the suite.

Rule: Call `t.Parallel()` at the top of every subtest that does not share
mutable state (package-level variables, shared servers, test databases,
on-disk fixtures). Top-level tests may also be parallel when they don't
depend on global state. When in doubt, don't parallelize — a correct slow
test beats a flaky fast one.

## Test Helpers

Reasoning: Test helpers that don't call `t.Helper()` produce misleading
failure line numbers. Helpers in production files pollute the public API.

Rule:

- All test helpers live in `_test.go` files.
- Helpers that call `t.Fatal` or `t.Error` must call `t.Helper()` as
  their first statement.
- Use setup helpers (e.g. `registerPlugin(...)`) to reduce repetition,
  not `TestMain` unless truly necessary.

## Unexported Types, Exported Factories

Reasoning: Callers should work with behavior, not struct fields. Keeping
concrete types unexported prevents direct construction and field access,
making the public API surface deliberately narrow.

Rule: Generic types are unexported. Callers obtain instances only through
exported factory functions that validate and initialize state.

```go
// ✅ Unexported concrete type, exported factory
type signalOf[T any] struct { ... }
func Signal[T any](c *Context, initial T) *signalOf[T] { ... }

// ❌ Exported struct handed directly to callers
type Signal[T any] struct { ID string; Val T }
```

## Plugin Constructor Naming

Reasoning: A uniform constructor name across all plugin packages makes the
API predictable and call sites consistent.

Rule: Every plugin package exposes `Plugin(...)` as its public constructor,
not `New(...)`. This keeps `via.WithPlugins(...)` call sites uniform.

```go
// ✅
via.WithPlugins(picocss.Plugin(), echarts.Plugin())

// ❌
via.WithPlugins(picocss.New(), echarts.Plugin())
```

## Functional Options

Reasoning: Optional configuration passed as variadic arguments keeps
call sites clean and avoids boolean-laden signatures. Conflicting options
are a programming error, not a runtime condition.

Rule: Optional config uses the `func(*cfg)` pattern. When two options
are mutually exclusive, the second one panics — fail at registration /
init time rather than silently overriding or returning an error. This
applies to options evaluated during startup; options resolved at runtime
should return errors instead.

```go
type StateOption func(*stateConfig)

func WithScopeApp() StateOption {
    return func(cfg *stateConfig) {
        if cfg.scopeSet {
            panic("conflicting scopes: multiple scope options provided")
        }
        cfg.scope = ScopeApp
        cfg.scopeSet = true
    }
}
```

## Panic on Invalid Registration

Reasoning: Errors during page or plugin registration are programming
mistakes, not recoverable runtime conditions. Panicking at startup makes
misconfiguration impossible to miss and impossible to ship.

Rule: Validation that runs once at registration time (inside `Page(...)`,
`Plugin(...)`, etc.) panics on invalid input. Do not return errors from
registration functions.

- ✅ Panic if `View` is never set, if conflicting options are passed, if
  required arguments are zero values.
- ❌ Return `error` from `Page(...)` and let callers ignore it.

## Assertions

Rule: Use `github.com/stretchr/testify/assert` for all assertions.

- Use `require` for preconditions that guard the rest of the test (nil
  checks, status codes before reading a body, setup that must succeed).
  Use `assert` for the actual behavioral claims — the assertions you
  want to see all of, even when one fails.
- Use `assert.JSONEq` for JSON comparison — it is order-insensitive.
- Use `assert.Contains` for partial string/slice membership.
- Do not use raw `t.Error`, `t.Fatal`, or `t.Log` for assertion failures —
  use testify.

## Comments

Comments explain **why**, never **what**. If a comment restates what the
code already says, delete it.

### Exported symbols

Document every exported type, function, method, and constant with a godoc
comment. The comment must add information beyond the name — if the name is
fully self-explanatory, one sentence stating the contract or caveat is
enough. Omit filler like "X is a…" when the sentence reads better without
it.

```go
// ✅ Adds information the name doesn't
// MustJSON marshals v to JSON, returning "null" on error.
func MustJSON(v any) string

// ✅ States a non-obvious contract
// AppendData drops the update silently when the SSE buffer is full.
func (c *Chart) AppendData(ctx *via.Context, data [][]any)

// ❌ Restates the name
// WithTitle sets the chart title.
func WithTitle(title string) ChartOption
```

### Unexported symbols and inner logic

Omit comments on unexported types, fields, and functions whose purpose is
clear from their name and context. Add a comment only when the logic would
otherwise require the reader to reconstruct non-obvious reasoning:

- A constraint imposed by an external system or protocol
- A subtle invariant that must be preserved across edits
- A deliberate choice that looks wrong but isn't (with a pointer to why)

```go
// ✅ Non-obvious invariant
// underscore prefix keeps the name a valid JS identifier; dots are not allowed.
return fmt.Sprintf("echart_%d", c.seq)

// ❌ Obvious from context
// increment the counter
chartCounter.Add(1)
```

### Tests

Test functions are named as behavioral claims — that name is the comment.
Do not add a prose comment above a test function. Do not add inline
comments that describe what an assertion checks; the assertion itself and
the `assert` message parameter serve that purpose.

Add a comment inside a test only when the **setup** involves a non-obvious
precondition whose absence would make the test logic misleading:

```go
// ✅ Non-obvious precondition
// Two charts share a page; both must render without ID collision.
c1 := echarts.NewChart()
c2 := echarts.NewChart()

// ❌ Describes what the next line already says
// Create a new chart with a line type.
chart := echarts.NewChart(echarts.WithChartType(echarts.TypeLine))
```

## Errors

Reasoning: Errors in Go are values. A consistent strategy for when to
return them, how to create them, and when to panic instead keeps the
codebase predictable.

### Panic vs. Return

Rule: Panic for programming mistakes caught at init / registration time.
Return errors for failures that can occur at runtime.

- **Panic**: nil arguments to constructors, conflicting options,
  missing required config, unrecoverable infrastructure failures
  (`rand.Read`).
- **Return error**: network calls, I/O, user-triggered actions, anything
  that depends on external state.

### Creating Errors

Rule: Use `fmt.Errorf` with a short prefix indicating origin. Do not
wrap with `%w` unless the caller needs `errors.Is` / `errors.As` — most
internal errors are terminal and wrapping adds noise.

```go
// ✅ Clear origin, no unnecessary wrapping
return fmt.Errorf("pico: fetch %s: status %d", url, resp.StatusCode)

// ❌ Wrapping an error nobody will unwrap
return fmt.Errorf("pico: fetch failed: %w", err)
```

### No Custom Error Types

Rule: Use plain `error` values. Do not introduce sentinel errors or
custom types unless there is a concrete caller that switches on them.
Premature error taxonomy is a form of speculative abstraction.

## Package and File Organization

Reasoning: Small, focused files grouped by responsibility are easier to
navigate than one-type-per-file sprawl or monolithic files that do
everything.

### Packages

Rule: A package owns one concept. Split a package when it has two
unrelated responsibilities, not when it gets large. A 500-line package
with one clear purpose is better than five 100-line packages with
circular concerns.

### Files

Rule: Group by responsibility, not by type. A file contains the types,
functions, and methods that serve a single concern. Name files after the
concern, not the type (`state.go`, not `signal_of.go`).

Split a file when it exceeds ~300 lines or when it contains two concerns
that change for different reasons. Don't split preemptively.

### Internal Packages

Rule: Use `internal/` for code that must not be imported by consumers
but is shared across packages within the module. Do not use `internal/`
as a dumping ground — the same responsibility rules apply.
