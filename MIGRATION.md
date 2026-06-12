# Migration guide

Every breaking change to Via's public API is recorded here, in the
same PR that lands the break. Each entry is an anchor heading that CI
couples to the apidiff report: a public break with no matching anchor
fails the build.

Entry format:

- A `###` heading naming the symbol(s) that changed.
- One paragraph on why the break exists.
- A before/after pair of Go snippets that both compile (the snippet
  gate compiles them like any other doc fence).

Entries are grouped newest-first under the release that ships them.

## v0.7.0

### `NumOps.Min` / `NumOps.Max` removed — use `AtLeast` / `AtMost` / `Clamp`

The `Min` / `Max` verbs had semantics inverted relative to the
`math.Min` / `math.Max` intuition their names invite: `Min(lo)` *raised*
the value to at least `lo`, and `Max(hi)` *lowered* it to at most `hi`.
They are replaced by verbs whose names state the effect — `AtLeast(lo)`,
`AtMost(hi)`, and `Clamp(lo, hi)`, which confines the value to
`[lo, hi]` and panics on inverted bounds (`lo > hi` is a programming
mistake, not a runtime condition).

Before:

```go
type Counter struct {
	Hits via.StateTabNum[int]
}

func (c *Counter) Bound(ctx *via.Ctx) {
	c.Hits.Op(ctx).Min(0)   // raised the value to at least 0
	c.Hits.Op(ctx).Max(100) // lowered the value to at most 100
}
```

After:

```go
type Counter struct {
	Hits via.StateTabNum[int]
}

func (c *Counter) Bound(ctx *via.Ctx) {
	c.Hits.Op(ctx).AtLeast(0)    // raise the value to at least 0
	c.Hits.Op(ctx).AtMost(100)   // lower the value to at most 100
	c.Hits.Op(ctx).Clamp(0, 100) // or confine both ends in one verb
}
```
