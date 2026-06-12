---
title: Rendering (h)
layout: default
parent: Guides
nav_order: 3
---

# Rendering with the `h` DSL
{: .no_toc }

1. TOC
{:toc}

`h` is the HTML DSL — elements, attributes, text, iteration, conditionals,
static pre-render, and custom tags, all as Go. `View` returns an `h.H`.

```go
h.Div(h.Class("card"),
    h.H1(h.T("Title")),
    h.Each(items, func(it Item) h.H { return h.Li(h.T(it.Name)) }),
)
```

Text and attribute values are HTML-escaped. The full per-symbol reference is
in [h helpers reference](h-helpers) and in
[`go doc .../h`](https://pkg.go.dev/github.com/go-via/via/h).

## Elements, attributes, text

Elements are constructors (`h.Div`, `h.Span`, `h.Button`, …) that take any
mix of attribute and child nodes; attributes are reordered into the open
tag automatically. `h.Text` / `h.T` escape their input; `h.Textf` formats;
`h.Raw` passes pre-trusted markup through unescaped.

```go
h.Input(h.Type("email"), h.Placeholder("you@example.com"), h.Required())
h.A(h.Href("/docs"), h.T("Docs"))
```

## Iteration and conditionals

```go
h.Each(items, func(it Item) h.H { return h.Li(h.T(it.Name)) })
h.EachIndexed(items, func(i int, it Item) h.H { ... })
h.If(loggedIn, h.Span(h.T("Welcome")))
h.IfElse(ok, yesNode, noNode)
h.Switch(key, h.Case(k1, n1), h.Default[K](fallback))
h.Fragment(nodeA, nodeB)   // group nodes without a wrapper element
```

`Fragment` takes content nodes only. Passing an attribute (`h.ID`,
`h.Class`, `on.*`, …) panics at construction — a fragment has no opening
tag to receive it; attach attributes to their element directly.

## Static pre-render

`h.Static(n)` pre-renders a fragment that doesn't depend on per-request
state (layout chrome, headers); every later Render writes the captured bytes
verbatim. See `BenchmarkSysmonShape_staticChrome_render` for the per-tick
allocation delta against rebuilding the same chrome on every tick.

```go
var chrome = h.Static(h.Header(h.H1(h.T("Dashboard"))))
```

## Custom tags and extension

```go
h.NewTag("my-widget")   // constructor for tags outside the built-in list
h.NewVoidTag("br-ish")  // self-closing variant
h.With(base, more...)   // extend an existing element non-destructively
```

`h.NewTag` covers web components, SVG, and MathML. `h.With` returns a new
element with extra children/attributes appended, leaving the base unchanged.

## Datastar attribute helpers

Beyond the `Signal[T]` view helpers ([Reactive state](reactive-state)), the
`h` package exposes raw Datastar bindings for frontend-only behaviour:

```go
h.DataOnClick("$open = !$open")          // data-on:click
h.DataClass("active", "$tab === 'home'") // data-class:active
h.DataShow("$open")                      // data-show
h.DataInit("$progress = '100%'")         // data-init (bare % is not mangled)
h.DataIgnoreMorph()                      // skip morphing this element
```

Use `on.Click(method)` for server actions; use `h.DataOnClick(expr)` for
client-only signal mutations.
