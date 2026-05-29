---
title: "Tutorial: build a todo app"
parent: Learn
nav_order: 2
---

# Tutorial: build a todo app
{: .no_toc }

The [counter](getting-started) showed the shape of a composition. This builds
something with real moving parts: a **session-backed** todo list that
survives a reload, an input bound to a client signal, list rendering with
`h.Each`, and two actions. About 20 minutes.

The finished app mirrors
[`internal/examples/todos`](https://github.com/go-via/via/tree/main/internal/examples/todos).

1. TOC
{:toc}

## 1. The composition

Two fields capture the whole client/server split:

```go
type Todos struct {
    // Server-owned, per session: survives a reload, scoped to the browser.
    Items via.StateSess[[]string]

    // Client-owned: the text being typed, bound to the <input>.
    Draft via.SignalStr `via:"draft"`
}
```

`Items` is `StateSess` because the list is server truth that should outlive a
page reload but stay private to one user's session. `Draft` is a `Signal`
because the in-progress text is pure client state — typing it should not POST.

## 2. The view

```go
func (t *Todos) View(ctx *via.CtxR) h.H {
    return h.Div(
        h.Input(t.Draft.Bind(), h.Placeholder("What needs doing?")),
        h.Button(h.Text("Add"), on.Click(t.Add)),
        h.Button(h.Text("Clear"), on.Click(t.Clear)),
        h.Ul(h.Each(t.Items.Read(ctx), func(item string) h.H {
            return h.Li(h.Text(item))
        })),
    )
}
```

`t.Draft.Bind()` is two-way: keystrokes update the client signal with no
round-trip. `h.Each` renders one `<li>` per item; because `Items` is
server-owned, the list re-renders server-side and ships a DOM patch over SSE
whenever it changes.

{: .note }
`View` takes `*via.CtxR` — a read-only render context. You can `Read` state
here but not mutate it. Mutations live in actions (next), which take the
full `*via.Ctx`.

## 3. The actions

```go
func (t *Todos) Add(ctx *via.Ctx) error {
    text := strings.TrimSpace(t.Draft.Read(ctx))
    if text == "" {
        return nil
    }
    if err := t.Items.Update(ctx, func(cur []string) ([]string, error) {
        return append(cur, text), nil
    }); err != nil {
        return err
    }
    t.Draft.Write(ctx, "") // clear the input client-side after a successful add
    return nil
}

func (t *Todos) Clear(ctx *via.Ctx) error {
    return t.Items.Update(ctx, func([]string) ([]string, error) {
        return nil, nil
    })
}
```

`Add` reads the current client `Draft`, appends to the server list under
`Update`'s per-key mutex, then writes `Draft` back to `""` — that empty value
flushes to the browser and clears the input. `Clear` replaces the list with
`nil`. Both are ordinary methods; `on.Click` binds them with compile-time
typo protection.

## 4. Wire it up

```go
func main() {
    app := via.New()
    via.Mount[Todos](app, "/")
    _ = http.ListenAndServe(":3000", app)
}
```

```bash
go run .
# open http://localhost:3000
```

Add a few items, then **reload the page**. The list is still there — that's
`StateSess` surviving the reload via the `via_session` cookie. Open a private
window and you get an empty list: the session scope is per-browser.

## 5. Where to go next

- **Bundle a client write with the action.** `on.SetSignal(&t.Draft, "")`
  sets a signal *before* the POST fires — handy when the value should change
  client-side regardless of the server result. (Here we cleared *after* a
  successful add instead, so an empty draft isn't lost on error.)
- **Typed ops.** Swap `StateSess[[]string]` for `via.StateSessSlice[string]`
  and call `t.Items.Op(ctx).Append(text)` / `.Empty()` instead of `Update`.
  See [Reactive state](reactive-state#typed-ops-via-opctx).
- **Remove a single item.** Add a per-row button that bundles the row's index
  with `on.SetSignal`, then read it in a `Remove` action.
- **Style it.** Add `picocss.Plugin()` ([Plugins](plugins)) for instant CSS.
- **Test it.** Drive the actions over HTTP with `vt` ([Testing](testing)).
