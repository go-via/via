---
title: h helpers reference
layout: default
parent: Reference & ops
nav_order: 3
---

# `h` package helpers

Full reference for [`github.com/go-via/via/h`](https://pkg.go.dev/github.com/go-via/via/h).
Grouped by responsibility; `go doc github.com/go-via/via/h` has each
symbol's contract.

## Iteration

- `h.Each(items, fn)` — one node per slice element, nil-pruned.
- `h.EachIndexed(items, fn)` — same with `(i, v)` passed to fn.
- `h.EachSeq(seq, fn)` — `iter.Seq` variant (`slices.Values`,
  `maps.Values`, …).
- `h.EachSeq2(seq, fn)` — `iter.Seq2` variant (`slices.All`,
  `maps.All`, …).

## Conditional

- `h.If(cond, n)`, `h.IfElse(cond, then, els)` — eager.
- `h.When(cond, build)`, `h.WhenElse(cond, then, els)` — lazy; only
  the winning branch is constructed.
- `h.Maybe(v, fn)` — render `fn(v)` only when v ≠ zero(T) (T must be
  `comparable`).
- `h.Switch(value, h.Case(...), h.Default[K](...))` — tab-style
  equality; `value` and every `Case` key share one comparable type `K`.
  `Default` needs the type spelled out (nothing to infer it from).
- `h.IfStr(cond, s)` — `s` if cond, `""` otherwise; pairs with
  `h.Class` and `h.Styles`.

## Composition

- `h.Fragment(items...)` — bundle many nodes into one `h.H`. Pass a
  slice with `items...`. Content nodes only: an attribute argument
  panics at construction (a fragment has no tag to receive it).
- `h.With(base, more...)` — return a copy of `base` extended with
  `more`. Non-destructive; supports chaining without variadic
  signatures.
- `h.Static(n)` — pre-render `n` once into bytes; every later Render
  writes them verbatim. Use for layout chrome that doesn't depend on
  per-request state. See [Held fragments](#held-fragments) below.

## Attributes

- `h.Class(parts...)` — variadic class names; empty parts skipped;
  returns nil (omits the attribute) when nothing remains.
- `h.Classes(parts...)` — deprecated alias for `h.Class`; kept so a
  slice in hand can spread without a rename.
- `h.ClassMap(m)` — emit each true key in sorted order.
- `h.Style(v)` — inline `style="..."` attribute. For
  `<style>...</style>` use `h.StyleEl`.
- `h.Styles(parts...)` — join non-empty CSS declarations with `;` and
  emit one inline `style` attribute.
- `h.Checked()`, `h.Required()`, `h.Disabled()`, `h.Selected()` —
  boolean attributes (`<input required>`).
- `h.Role`, `h.Min`, `h.Max`, `h.Step`, `h.For`, `h.Lang`,
  `h.Content`, `h.Charset` — common single-string attributes.

## Custom tags

- `h.Tag(name, children...)`, `h.VoidTag(name, children...)` — escape
  hatch for tags absent from the static list (web components, SVG).
  The name is written verbatim; callers remain responsible for
  validity.
- `h.NewTag(name)`, `h.NewVoidTag(name)` — reusable constructors with
  the same shape as the built-ins.

## Text

- `h.Text(s)`, `h.T(s)` — HTML-escaped text node (`T` is the short
  alias). `h.Textf(f, args...)` formats first.
- `h.Raw(s)` — emit `s` verbatim without escaping. Caller-trusted.
- `h.RawAttr(name, value)` — emit a raw `name="value"` attribute pair
  without escaping the value. The sanctioned plugin escape hatch for
  attribute-shaped output (the `attribute` marker is unexported on
  purpose — see `on` for the canonical pattern).

## Held fragments

For fragments that don't change between renders, `h.Static` pre-renders
once and writes the captured bytes on every later Render — no
per-tick allocation for the chrome subtree:

```go
chrome := h.Static(h.Fragment(
    h.Nav(h.Class("container-fluid"),
        h.Ul(h.Li(h.Strong(h.T("System Monitor"))))),
))

func (p *Page) View(ctx *via.CtxR) h.H {
    return h.Div(chrome, p.body(ctx))
}
```

`internal/examples/sysmon` uses this pattern; the
`BenchmarkSysmonShape_staticChrome_render` bench shows the per-tick
allocation win versus rebuilding the same chrome on each tick.

## Custom elements

For tags absent from the static constructor list — web components,
SVG, MathML — declare them once with `h.NewTag` (or `h.NewVoidTag` for
void elements):

```go
var SVG = h.NewTag("svg")
SVG(h.Attr("xmlns", "http://www.w3.org/2000/svg"), shapes...)
```

The tag name is written verbatim; supply a valid HTML element name.
