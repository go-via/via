# Conventions

## Test Names

Reasoning: Consistent naming makes tests discoverable and clarifies what
each test verifies.

Rule: Use `Test` + camelCase with present tense verbs. Use underscores to
separate subject from behavior.

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

Rule: Never mock what you own. Mock only at the edge of the system — where
Go code meets something outside its process.

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
mutable state. Top-level tests may also be parallel when they don't depend
on global state.

## Test Helpers

Reasoning: Test helpers that don't call `t.Helper()` produce misleading
failure line numbers. Helpers in production files pollute the public API.

Rule:

- All test helpers live in `_test.go` files.
- Helpers that call `t.Fatal` or `t.Error` must call `t.Helper()` as
  their first statement.
- Use setup helpers (e.g. `registerPlugin(...)`) to reduce repetition,
  not `TestMain` unless truly necessary.

## Assertions

Rule: Use `github.com/stretchr/testify/assert` for all assertions.

- Prefer `assert` (non-fatal) over `require` unless the test cannot
  meaningfully proceed after a failure (e.g., dereferencing a potentially
  nil pointer on the next line).
- Use `assert.JSONEq` for JSON comparison — it is order-insensitive.
- Use `assert.Contains` for partial string/slice membership.
- Do not use raw `t.Error`, `t.Fatal`, or `t.Log` for assertion failures —
  use testify.
