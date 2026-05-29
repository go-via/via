---
title: Examples
nav_order: 3
---

# Examples

Eleven runnable example apps ship in
[`internal/examples/`](https://github.com/go-via/via/tree/main/internal/examples).
Each is a single `main.go` you can read in a sitting and run directly:

```bash
go run ./internal/examples/counter
# then open http://localhost:3000 (or the port the example prints)
```

| Example | What it teaches |
|---|---|
| [counter](https://github.com/go-via/via/tree/main/internal/examples/counter) | `StateTab[int]` + `Signal[int]` + a typed action — the canonical first app. |
| [greeter](https://github.com/go-via/via/tree/main/internal/examples/greeter) | A `Signal[string]` mutated from two distinct actions. |
| [pathparams](https://github.com/go-via/via/tree/main/internal/examples/pathparams) | Typed `path:"id"` decoding into composition fields. |
| [countercomp](https://github.com/go-via/via/tree/main/internal/examples/countercomp) | Two independent counter compositions nested on one page; isolation across instances. |
| [counterscope](https://github.com/go-via/via/tree/main/internal/examples/counterscope) | `StateTab[int]` (tab-local) vs `StateApp[int]` (shared across every session) side by side. |
| [picocss](https://github.com/go-via/via/tree/main/internal/examples/picocss) | `picocss.Plugin()` driving theme + dark-mode switching on the client without a reload. |
| [auth](https://github.com/go-via/via/tree/main/internal/examples/auth) | Typed sessions, `requireAuth` middleware, and `sess.Rotate` after login. |
| [todos](https://github.com/go-via/via/tree/main/internal/examples/todos) | `StateSess[T]` survives reload, `h.Each`, and `on.SetSignal` for client-bundled writes — the basis of the [tutorial](tutorial). |
| [sysmon](https://github.com/go-via/via/tree/main/internal/examples/sysmon) | An `OnConnect`-driven ticker streaming CPU / RAM / disk / net into ECharts, with a pause + interval-slider UI via `via.Ticker`. |
| [upload](https://github.com/go-via/via/tree/main/internal/examples/upload) | A `via.File` field bound to a `multipart/form-data` `<form>`, persisted to disk with a redirect back. |
| [feed](https://github.com/go-via/via/tree/main/internal/examples/feed) | An append-only / bounded-ring slice stream driven by `Signal[[]T].Update`, paused and cleared from actions. |

New to Via? Read [Getting started](getting-started), then walk through the
[todo-app tutorial](tutorial).
