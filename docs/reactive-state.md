---
title: Reactive state
layout: default
parent: Learn
nav_order: 3
---

# Reactive state
{: .no_toc }

1. TOC
{:toc}

## The four reactive shapes

Whether a piece of state lives on the client, the server, or both is the
field's type — not a convention.

| Handle | Scope | Lives on |
|---|---|---|
| `via.Signal[T]` | per-tab | client + server |
| `via.StateTab[T]` | per-tab | server only |
| `via.StateSess[T]` | per-session | server only |
| `via.StateApp[T]` | global | server only |

- `Signal[T]` is mirrored into the browser by Datastar, the runtime that keeps
  the page reactive and updates it in place. Bind it to inputs and view
  helpers; it reacts client-side with no round-trip.
- `StateTab[T]` / `StateSess[T]` / `StateApp[T]` live only in Go. They
  change through actions, and a re-render re-emits the value over SSE.

Scope at a glance — one process, many sessions, many tabs:

```
   App (one process) ── StateApp[T] ............... shared by everyone
    │
    ├─ Session A (one browser) ── StateSess[T] ..... shared by that user's tabs
    │   ├─ Tab 1 ── Signal[T] (client) + StateTab[T] (server)
    │   └─ Tab 2 ── Signal[T] + StateTab[T]   ← its own copy
    │
    └─ Session B (another browser) ── StateSess[T]
        └─ Tab 1 ── Signal[T] + StateTab[T]
```

`Signal[T]` also lives in the browser; the three `State*` shapes never leave
the server.

## Reads, writes, and updates

Reads go through `Read(ctx)`; writes through `Update(ctx, fn)`. `Signal[T]`
and `StateTab[T]` also expose `Write(ctx, v)` for direct sets — per-tab
writes are already serialized by the action mutex.

```go
n := c.Hits.Read(ctx)

c.Hits.Write(ctx, 0)                       // Signal / StateTab only

err := c.Hits.Update(ctx, func(n int) (int, error) {
    return n + 1, nil                      // load → fn → store, under a per-key mutex
})
```

`Update` holds a per-key mutex across the load → fn → store sequence, so
concurrent writers from different ctxs cannot lose increments. If `fn`
returns an error the store is left unchanged, no broadcast fires, and the
error is returned.

{: .note }
`StateApp[T]` has no `Write` — a blind write on shared state is almost
always a read-modify-write race in disguise. Model the assignment as an
`Update` whose `fn` ignores the old value if you truly mean it. Calling
`Update` with a nil `*Ctx` panics: without one, no broadcast can fan out.

## Typed ops via `Op(ctx)`

For the common shape buckets — numeric, bool, string, slice, map — use the
`Num` / `Bool` / `Str` / `Slice` / `Map` typed wrappers and call `Op(ctx)`
for shape-aware verbs. Drop back to `Update(ctx, fn)` for custom transforms
or non-bucket `T` (structs, interfaces).

| Field type | Common verbs |
|---|---|
| `via.StateTabNum[int]` | `Add(n) / Sub(n) / Inc() / Dec() / Zero() / AtLeast(lo) / AtMost(hi) / Clamp(lo, hi)` |
| `via.SignalBool` | `Toggle() / True() / False()` |
| `via.StateSessStr` | `Append(s) / Prepend(s) / Clear()` |
| `via.SignalSlice[T]` | `Append(v) / Prepend(v) / Pop() / Shift() / Take(n) / Drop(n) / Filter(pred) / Empty()` |
| `via.StateAppMap[K,V]` | `Put(k,v) / Delete(k) / Empty()` |

```go
c.Hits.Op(ctx).Add(1)
c.Open.Op(ctx).Toggle()
c.Items.Op(ctx).Append(item)
```

## View helpers driven by `Signal[T]`

`Signal[T]` mirrors into the browser's reactive graph. These helpers compile
to Datastar `data-*` attributes that subscribe to it — DOM updates are
fine-grained, with no re-render and no round-trip:

```go
s.Bind()              // <input data-bind="key"> two-way binding
s.Text()              // data-text="$key" attribute — attach to a host element
s.TextSpan()          // <span data-text="$key"></span> — standalone span
s.Show()              // data-show="$key" — toggle display by truthiness
s.Attr("disabled")    // data-attr:disabled="$key" — drives an HTML attr
s.Style("color")      // data-style:color="$key" — drives an inline CSS prop
```

`StateTab[T]` / `StateSess[T]` / `StateApp[T]` share `Text(ctx)`, which
re-renders server-side instead of subscribing to a client signal.

## Wire keys and init values

The `via:"name,init=..."` tag sets the wire key and an initial value.
A tagless field uses the lower-cased field name as its key. `init=` values
are decoded into the field's type (int, uint, float, bool, string). Wire
keys, initial values, and the full tag grammar are documented on the
[`Signal` type in godoc](https://pkg.go.dev/github.com/go-via/via#Signal).

```go
type Page struct {
    Theme via.Signal[string]    `via:"theme,init=auto"`
    Step  via.SignalNum[int]    `via:"step,init=1"`
    Open  via.SignalBool        // key defaults to "open"
}
```

## Across the cluster

The four shapes above are per-pod by default — `StateApp[T]` lives in the
process that serves the tab. Wire in a [backplane](distributed-state) and
`StateApp`/`StateSess` converge across every pod and survive a restart with the
**same API**, and a new opt-in sibling — `StateAppEvents[E, V]` — carries
high-churn shared state as an append-only event log. See
[Distributed state](distributed-state).
