---
title: Compositions
layout: default
parent: Guides
nav_order: 2
---

# Compositions
{: .no_toc }

1. TOC
{:toc}

A composition is a Go struct with reactive state as fields, actions as
methods, and a `View` — see [Getting started](getting-started) for the
single-composition case. This page is about putting compositions
*together*: nesting them, passing data down, and routing events back up.

## Nesting

A child composition is an exported pointer field to a struct that has a
`View` method. `Mount` discovers it by reflection, allocates it, and folds
its state into the page — you never register a child separately. The
pointer is mandatory: the runtime binds each handle by address, so `Mount`
panics on a child held by value.

```go
type CounterCard struct {
    Count via.StateTabNum[int]
    Step  via.SignalNum[int] `via:"step,init=1"`
}

type Page struct {
    A *CounterCard
    B *CounterCard
}
```

Each child's reactive state gets a wire key prefixed by the field name —
`A.count`, `A.step`, `B.count`, `B.step` — so two instances of the same
type never collide. Instance isolation is automatic; you don't assign ids.
The runnable version is
[`internal/examples/countercomp`](https://github.com/go-via/via/tree/main/internal/examples/countercomp).

## One tree, one render

The whole composition tree is allocated once when the tab loads and lives
for the tab's lifetime. The framework drives only the *root* (the mounted
composition): its `View`, its lifecycle hooks, and its actions. A child's
methods are ordinary Go — nothing calls them but the parent.

So the parent renders a child by calling its `View` from its own `View`,
and every flush re-runs the root `View` top-down. There is no per-child
re-render boundary: when state changes, the page re-renders and the morph
diff updates only what actually moved.

## Passing props (parent → child)

Because the parent calls the child's `View` directly, **props are just
`View` parameters** — plain Go arguments, checked by the compiler, with no
reflection or wire protocol. They are recomputed on every parent re-render,
so a prop derived from parent state stays reactive for free.

```go
// child — View takes whatever the parent decides to hand it
func (c *CounterCard) View(ctx *via.CtxR, title string, onClick h.H) h.H {
    return h.Div(
        h.H2(h.Text(title)),
        h.P(h.Textf("Count: %d", c.Count.Read(ctx))),
        h.Button(h.Text("Increment"), onClick),
    )
}

// parent — passes title and the click handler down
func (p *Page) View(ctx *via.CtxR) h.H {
    return h.Div(
        p.A.View(ctx, "Counter 1", on.Click(p.IncA)),
        p.B.View(ctx, "Counter 2", on.Click(p.IncB)),
    )
}
```

Put `ctx` first, then the props. Event handlers are props too: `on.Click(…)`
returns an `h.H` the parent hands down, which is how a child button drives a
parent action.

{: .note }
A child's `View` signature is yours to shape. Only the *mounted* (root)
composition's `View` is constrained to `func(*via.CtxR) h.H` — a child is
never mounted, so its `View` can take any parameters you need.

### Per-instance initial state

The `via:"…,init=N"` tag sets a default per *type*, so every instance of a
child starts the same. When instances must start differently, seed the
child's state from the parent's `OnInit` — it runs once, before the first
render, and receives the full `*via.Ctx`:

```go
func (p *Page) OnInit(ctx *via.Ctx) error {
    p.A.Count.Write(ctx, 10) // A starts at 10
    p.B.Count.Write(ctx, 0)  // B starts at 0
    return nil
}
```

{: .warning }
Seed state in place; never reassign a child
(`p.A = &CounterCard{…}`). The runtime binds each handle's slot and wire key
*by address*, before `OnInit` runs — swapping in a fresh struct silently
orphans that binding. The seeded value still paints once, which makes the
bug look harmless, but client bindings (`Text`, `Bind`, `Show`, `Attr`) go
dead and later updates mis-route. The default dev check fails such a render
loudly. Set fields individually and seed handles with `.Write(ctx, …)`.

## Events (child → parent)

Actions dispatch by method name on the *root* composition, so a child's own
methods aren't reachable from the wire. Route a child event up through a thin
forwarding method on the parent:

```go
func (c *CounterCard) Inc(ctx *via.Ctx) {
    c.Count.Op(ctx).Add(c.Step.Read(ctx))
}

func (p *Page) IncA(ctx *via.Ctx) { p.A.Inc(ctx) }
func (p *Page) IncB(ctx *via.Ctx) { p.B.Inc(ctx) }
```

The parent owns one action per child instance and forwards to the child it
names. This keeps the wire surface explicit: every action the page can
receive is a method on the page.

## When to nest

Reach for a child composition when a piece of UI carries its own state and
appears more than once — a counter card, a row editor, a chart panel — and
you want each instance isolated without bookkeeping. For one-off layout that
holds no state, a plain `h.H` helper or an
[`h.Static`](h-helpers#composition) fragment is lighter than a composition.

See also [Actions & lifecycle](actions-and-lifecycle) for the hook contract
and [Reactive state](reactive-state) for how the typed handles behave.
