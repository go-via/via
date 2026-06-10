---
title: Examples
layout: default
nav_order: 3
---

# Examples

Thirteen runnable example apps ship in
[`internal/examples/`](https://github.com/go-via/via/tree/main/internal/examples).
Each is a single `main.go` you can read in a sitting and run directly:

```bash
go run ./internal/examples/chat
# open http://localhost:3000 in two browser windows — messages sync live
```

⭐ The flagship is
**[chat](https://github.com/go-via/via/tree/main/internal/examples/chat)** — a
live multi-user chatroom in ~60 lines of Go, no WebSocket and no client JS,
walked through step by step in the [tutorial](tutorial).

| Example | What it teaches |
|---|---|
| [chat](https://github.com/go-via/via/tree/main/internal/examples/chat) ⭐ | A live multi-user chatroom: an app-scoped event log (`StateAppEvents`) folds each `Posted` event and fans the new line out to every connected browser — no WebSocket, no JS. |
| [counter](https://github.com/go-via/via/tree/main/internal/examples/counter) | `StateTab[int]` + `Signal[int]` + a typed action — the canonical first app. |
| [greeter](https://github.com/go-via/via/tree/main/internal/examples/greeter) | A `Signal[string]` mutated from two distinct actions. |
| [pathparams](https://github.com/go-via/via/tree/main/internal/examples/pathparams) | Typed `path:"id"` decoding into composition fields. |
| [countercomp](https://github.com/go-via/via/tree/main/internal/examples/countercomp) | Two independent counter compositions nested on one page; isolation across instances. |
| [counterscope](https://github.com/go-via/via/tree/main/internal/examples/counterscope) | `StateTab[int]` (tab-local) vs `StateApp[int]` (shared across every session) side by side. |
| [picocss](https://github.com/go-via/via/tree/main/internal/examples/picocss) | `picocss.Plugin()` driving theme + dark-mode switching on the client without a reload. |
| [auth](https://github.com/go-via/via/tree/main/internal/examples/auth) | Typed sessions, `requireAuth` middleware, and `sess.Rotate` after login. |
| [todos](https://github.com/go-via/via/tree/main/internal/examples/todos) | `StateSess[T]` survives reload, `h.Each`, and `on.SetSignal` for client-bundled writes. |
| [sysmon](https://github.com/go-via/via/tree/main/internal/examples/sysmon) | An `OnConnect`-driven ticker streaming CPU / RAM / disk / net into ECharts, with a pause + interval-slider UI via `via.Ticker`. |
| [maps](https://github.com/go-via/via/tree/main/internal/examples/maps) | `maplibre.Plugin()` driving an interactive world map — city fly-to, a ticker-moved "drone", click-to-place pins, marker & feature clicks, hover highlight, and popups, all from Go over SSE. |
| [upload](https://github.com/go-via/via/tree/main/internal/examples/upload) | A `via.File` field bound to a `multipart/form-data` `<form>`, persisted to disk with a redirect back. |
| [feed](https://github.com/go-via/via/tree/main/internal/examples/feed) | An append-only / bounded-ring slice stream driven by `Signal[[]T].Update`, paused and cleared from actions. |

New to Via? Read [Getting started](getting-started), then walk through the
[live-chatroom tutorial](tutorial).
