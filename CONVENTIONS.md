# Conventions

## Test-First

Reasoning: Writing the test first forces you to define the contract before
the implementation, and every behavior ends up with a corresponding test.

Rule: No implementation before a failing test. The sequence is always:
write test → confirm it fails correctly → implement → confirm it passes.

## Test Scope: Outside-In Through the Public API

Reasoning: Tests coupled to internals break on refactors, not on
regressions. The public API is the contract — that's what matters.

Rule: All tests enter the system through exported symbols. Behaviour the
plain HTTP idiom can't reach cleanly (a controlled request Host, a TLS
request, the per-connection tab handshake, a live render) is exercised through
the `vt` harness, not by reaching into unexported state. If a behaviour cannot
be observed through the public surface even with `vt`, that is a design
signal: change the surface, not the test boundary.

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

## Test Helpers

Reasoning: Helpers in production files pollute the public API.

Rule:

- All test helpers live in `_test.go` files.
- Use setup helpers (e.g. `serveCounter(t)`) to reduce repetition,
  not `TestMain` unless truly necessary.
- Send test requests through `srv.Client()` or a cloned transport
  (`plainClient(t)`, `jarClient(t)` in the root package), never
  `http.DefaultClient`, `http.Get` or a `Client` with no `Transport`. Every
  `httptest.Server.Close` closes `http.DefaultTransport`'s idle connections,
  so a parallel test's teardown breaks the request. A lint enforces it.

## Functional Options

Reasoning: Optional configuration passed as variadic arguments keeps
call sites clean and avoids boolean-laden signatures. Conflicting options
are a programming error, not a runtime condition.

Rule: Optional config is an interface with one unexported method,
`apply(*config)`, and each constructor returns a small unexported type
that implements it. When two options are mutually exclusive, the second
one panics — fail at registration / init time rather than silently
overriding or returning an error. This applies to options evaluated
during startup; options resolved at runtime should return errors instead.

```go
type Option interface{ apply(*config) }

func WithDarkMode() Option { return darkMode{} }

type darkMode struct{}

func (darkMode) apply(cfg *config) {
    if cfg.themeSet {
        panic("via: conflicting theme options")
    }
    cfg.theme = "dark"
    cfg.themeSet = true
}
```

Rule: Export the option type; keep the config it mutates unexported and
in the same package. Callers must be able to name the option type — to
hold one in a variable, build a `[]Option` slice, or write a helper that
returns options. The `apply` method stays unexported because the option set
is closed: users compose the provided `WithX` constructors, they never author
an option by hand.

- ✅ `type Option interface{ apply(*config) }` — `config` unexported, same
  package
- ✅ `type MountOption interface{ applyMount(*mountConfig) }` — no
  constructors yet; it keeps `Mount`'s signature stable for when one lands

## Panic on Invalid Registration

Reasoning: Errors during page or router registration are programming
mistakes, not recoverable runtime conditions. Panicking at startup makes
misconfiguration impossible to miss and impossible to ship.

Rule: Validation that runs once at registration time (inside `Mount`,
`WithHead`, etc.) panics on invalid input. Do not return errors from
registration functions.

- ✅ Panic on conflicting options (two `WithSessionStore`) or a zero
  required argument (`WithMaxSSEConn(0)`). A root with no `View` never gets
  this far: `Mount`'s type constraint makes it a compile error.
- ❌ Return `error` from `Mount` and let callers ignore it.

Rule: Input that would otherwise fail later and somewhere else is checked at
registration too. A CSP that silently blocks a script in the browser, or a
ServeMux panic naming a wildcard the user never wrote, is a registration
error via missed.

- ✅ `Script{Src: "//cdn.example/x.js"}` panics at `Mount`: it has no scheme
  to take an origin from, so the policy would block it.
- ✅ `Mount(r, "/u/{child}", P{})` panics with via's message, not
  ServeMux's "duplicate wildcard name".
- ❌ Accept `FontOrigins: {"https://fonts.example/css"}` and let the browser
  ignore the source because it has a path.

## Duck-Typed Methods

Reasoning: via's duck-typed surface is deliberately tiny — `View`, `OnInit`,
`OnReload`, `PageMeta` — in two families: lifecycle phases, and document
queries. Every addition costs every reader a name they must know is magic.

Rule: A property becomes a duck-typed method only if it clears all three of
these:

1. It is a pure function of the type's own data (no arguments, no side
   effects, safe to call again).
2. It writes a slot shared by more than one unit, so an imperative setter
   would let any unit in the tree clobber it.
3. It is read exactly once per document render.

`PageMeta` clears all three, which is why it is a method on the root and not
`ctx.Title(…)`: the document head is one shared slot, and the setter shape let
any child silently rename the page.

Measured against the same rule and deliberately left imperative:

- `ctx.Tick` / `ctx.Listen` — per-unit registrations with no shared slot to
  clobber, and they are side effects, not queries (fails 1 and 2).
- `ctx.Redirect` — conditional, and not read once per render (fails 1 and 3).
- The `With*` options — router-wide policy: cookie policy, body caps and the
  CSP floor every mount shares. A page must not be able to change them, so
  they stay outside the composition entirely. A page widens its own CSP only
  through `Meta.Assets`.

Rule: A new head slot is a field on `via.Meta`, not a fifth method. There is
deliberately no `Description` or `Assets` sibling: `PageMeta` is the one
document-query hook.

Rule: Anything CSP-relevant is declared once, off the mounted literal, and
never derived from a request. `Meta.Assets` is the exception to rule 3: it
decides the mount's CSP, so via reads it three times — at `Mount` off the
mounted literal, at `Mount` off a probe copy with its zero fields filled in,
and on every document render — and panics if any two disagree. If `PageMeta`
panics on the probe's synthetic data, via logs a WARN and skips the probe,
leaving the render-time check to catch drift on the first request.

## Defaults Favour the Developer

Reasoning: A default that refuses what a developer does on their own machine
(a second port, a dev proxy, plain http on localhost) breaks every user's
first hour to close a risk that exists only once the app is deployed. A
warning at startup reaches the person who decides, when it matters, without
breaking the loop they are in.

Rule: A default never gets in the way of local development. Where a stricter
setting would, it is opt-in through a `With*` option, and while it is off,
via logs one WARN that says what the open default exposes and which option
closes it: from `NewRouter`, or on first use for a default only some apps
reach (the session key and store WARN on the first session minted).
Tightening applies only inside an option the user chose. An audit or review
finding against such a default is answered with a clearer warning or docs,
never a strict default: that is the maintainer's design call, not a fix.

- ✅ No `WithTrustedOrigin`: every origin is admitted, and startup warns that
  any site can fire an action and a same-site page can fire one as the
  signed-in user.
- ✅ Normalizing `WithTrustedOrigin` values, or refusing an http Origin
  behind TLS once it is set: stricter only where the user asked for it.
- ❌ Refusing `Sec-Fetch-Site: same-site` by default: every dev proxy and
  second localhost port answers 403.
- ❌ Answering a CSRF finding by enforcing the origin check without
  configuration.

## Browser-Parity Gates

Reasoning: A URL, origin or inline-asset check protects what the browser
does with the value, not what Go sees. Browsers drop tab, CR and LF anywhere
in a URL, read `\` as `/`, and normalize CRLF and NUL before hashing an
inline script. A gate that checks the raw string passes values the browser
then treats as something else.

Rule: Each gate that admits a value into a page, a redirect or a request
lives once, in `internal/render`, and every caller uses that copy. It
normalizes input the way the browser's parser does before deciding. Its test
is a table of inputs that differ only after browser normalization. A
boot-time check that can only refuse more, like `head.go` parsing
`Meta.Assets` URLs with `url.Parse`, may sit with its caller.

- ✅ `SafeURL` strips `\t\r\n` first, so `"/\t/evil.com"` reads as
  protocol-relative and is refused.
- ❌ A second URL check that trims only leading whitespace and admits what
  `SafeURL` refuses.

## Response Ownership

Reasoning: The POST handler and the tab's stream goroutine both touch the
request's `http.ResponseWriter` (a session cookie is set through it). Once
net/http has the handler's return, any later write races its teardown and
can crash the process.

Rule: Whoever holds the writer keeps the request alive until done with it.
After an action is handed to the stream goroutine, the handler returns only
with that action's result. Deadlines bound the wait to be picked up, never
work that has started.

- ✅ An action picked up at 4.9s of a 5s deadline runs to completion and
  answers.
- ❌ Answer 503 at 5s while the action keeps running and writes a cookie.

## Assertions

Rule: Use `github.com/stretchr/testify/assert` for all assertions. The
importable helpers (`vt`, `vtbrowser`) fail through `testing.TB` instead, so
importing them doesn't pull testify into a user's build.

- Use `require` for preconditions that guard the rest of the test (nil
  checks, status codes before reading a body, setup that must succeed).
  Use `assert` for the actual behavioral claims — the assertions you
  want to see all of, even when one fails.
- Use `assert.JSONEq` for JSON comparison — it is order-insensitive.
- Use `assert.Contains` for partial string/slice membership.

## Comments

Comments explain **why**, never **what**. If a comment restates what the
code already says, delete it.

### Exported symbols

An exported symbol's godoc must add information beyond the name — if the
name is fully self-explanatory, one sentence stating the contract or caveat
is enough. Omit filler like "X is a…" when the sentence reads better without
it.

```go
// ✅ Adds information the name doesn't
// ErrNotFound from OnInit or OnReload answers 404 rather than a 500.
var ErrNotFound = errors.New("via: not found")

// ✅ States a non-obvious contract
// Redirect navigates after the current handler returns. A target that is
// not http(s) or same-origin relative is dropped and logged, never followed.
func (c *Ctx) Redirect(path string)

// ❌ Restates the name
// WithMaxBody sets the max body.
func WithMaxBody(bytes int64) Option
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
// Datastar's fetch filter excludes /(^|\.)_/, so only a leading underscore
// keeps a nested or child-scoped signal off the POST.
return "_" + prefix + f.name

// ❌ Obvious from context
// increment the counter
counter.Add(1)
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
// Two fields of one child type share a parent; their slots must not collide.
p := pair{A: note{}, B: note{}}

// ❌ Describes what the next line already says
// Create a new router.
r := via.NewRouter()
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

Split a file when it contains two concerns that change for different
reasons — not when it merely gets large. Don't split preemptively.

### No tests of package main

Reasoning: package main cannot be imported, so a test of it has to sit inside
it, which is a white-box test by another name.

Rule: Keep `main` to wiring: flags, environment, calling into a package. The
logic goes in an importable package with its own `package <x>_test` tests.

- ✅ `content/gen/main.go` calls `apigen`, and `apigen_test.go` tests it.
- ❌ The generator's logic in `content/gen`, tested from `package main`.

### Internal Packages

Reasoning: Go keeps a type's methods in the type's package, so every public
type, and the code bound to it, stays in root `via`. Code that never touches
those types can still be fenced off, and a package boundary is the only fence
the compiler checks.

Rule: Use `internal/` for code that must not be imported by consumers and
either is shared across packages within the module, or is split out of root
to hide one decision. Do not use `internal/` as a dumping ground — the same
responsibility rules apply.

- ✅ `internal/render` holds the binder plumbing that root, `h` and `on`
  share.
- ✅ A package that owns the session cookie and blob layout, imported only
  by root.
- ❌ An `internal/util` that collects helpers from several concerns.

## Behaviour Changes Carry Their Docs

Reasoning: via documents behaviour in four places: the exported doc comment,
`CHANGELOG.md`, `MIGRATION.md` and the site. A fix that updates only the doc
comment leaves the others contradicting it, and nothing fails.

Rule: A commit that changes observable behaviour updates each place that
describes it: the doc comment always, the unreleased `CHANGELOG.md` entry,
`MIGRATION.md` when an upgrading user must act, and the site page that
covers it. A test that pins a known defect names it as a defect in its
comment, and the fix flips or deletes that test in the same commit.

- ✅ Changing the `WithMaxBody` default touches its doc comment, the
  CHANGELOG entry, MIGRATION's upgrade notes and the site reference row.
- ❌ Update the doc comment and leave `MIGRATION.md` describing the old
  default.

Rule: `README.md` describes no behaviour on its own. It holds the compiled
program, claims that hold across releases, and links to the page that owns
each detail. Tests pin its program to the snippet, its Go version to
`go.mod`, and each go-via.dev link and anchor to a page the site serves.

- ✅ "A page is plain HTTP until something on it goes live; then its tab
  holds one stream", linking to /live.
- ❌ "The current release is v0.8.3", or a copy of /why's list of costs.

## Release Tags

Reasoning: Go reads a nested module's version only from a tag prefixed with
its directory. A module released without one has only pseudo-versions and no
pkg.go.dev page, and links to it break. The Go proxy keeps a pushed version
for good, so a tag cannot be taken back.

Rule: A release tags every importable module in the repo on the same commit,
annotated: `vX.Y.Z` for root and `<dir>/vX.Y.Z` for each nested module whose
path sits under `github.com/go-via/via`. Push all of a release's tags
together. A wrong tag is fixed by a new patch version, never by moving or
deleting it. A module with a path outside the repo's, like `internal/site`,
is deployed, not imported, so it gets no tag.

- ✅ `v0.9.1` and `vtbrowser/v0.9.1` on `6e37e04`.
- ❌ Tag `v0.9.2` alone: `go get .../vtbrowser@v0.9.2` fails, and
  pkg.go.dev shows `vtbrowser` at v0.9.1.

## Markdown

Reasoning: Consistent markdown style improves readability and enables linting.

### Tables

Rule: Avoid tables for large data; they force excessive line wrapping. Use
lists, prose, or code blocks instead.

### Lists

Rule: Surround lists with blank lines. Lists preceded by a paragraph need
a blank line before the first item.

### Headings

Rule: Use headings (`##`) instead of emphasis (`**text**`) to introduce
sections. Reserve emphasis for inline content within paragraphs.
