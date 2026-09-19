# Examples

Run any of them with `go run ./example/<name>`.

## Where to start

Read three, in this order — each adds exactly one idea to the last:

1. **`counter`** — the whole model with nothing live in it. A struct, a `View`
   method, a handler method, `via.On` wiring them. A click POSTs and the page
   morphs. Start here even if you only care about the live stuff.
2. **`greeting`** — adds `Signal`: state that lives in the BROWSER. `Bind()` on
   the input and `Display()` next to it share one wire name, so typing updates
   the text with no request at all. `Ref()` hands the same signal to `expr`, and
   `h.DataShow` puts the expression on an element.
3. **`pulse`** — adds `State` and the SSE stream: state that lives on the
   SERVER, pushed to the tab. This is where a page stops being plain.

After that, pick by what you need: `shared` for a value the app owns and every
tab tracks through `via.StateTrack` on the field, with no `OnInit` at all
(state shared across tabs — `State` alone is per connection), `feed` for
`Topic` + `ctx.Listen`, which is what you want when each event matters rather
than only the latest, `poll` for `via.OnArg` per-row
actions, `dashboard` for several live children on one stream plus a JS island
fed by `Set` and a client-only `SignalCS`, `chat` for both topic shapes at
once, `forum` for the multi-page/router/session/upload side.

| example | what it shows |
| --- | --- |
| `counter` | server-rendered state, one action, no client signal |
| `greeting` | a client-resident `Signal`, two-way bound, displayed, and tested by `expr` |
| `pulse` | a server tick pushed to one tab over SSE |
| `shared` | an app-owned counter every tab follows with `via.StateTrack` |
| `feed` | a `Topic` broadcast fanning out to every connected tab |
| `poll` | a reordering list where `via.OnArg` carries each row's id; `h.DataAttr` |
| `dashboard` | live regions on one stream; a canvas island; tag-seeded signals |
| `chat` | a `Topic` message bus plus a presence count tracked from `OnInit` |
| `forum` | a multi-page app: router, sessions, `PostForm` + `Redirect`, upload |

## Not shown here — read the godoc

These are documented API with no example to copy from. Go to `go doc` (and the
README section named) rather than hunting for one:

| feature | where |
| --- | --- |
| `Router.Close` and graceful shutdown ordering | `go doc via.Router.Close`, README "Shutdown" |
| `SessionStore` / `WithSessionStore` (durable, multi-pod sessions) | `go doc via.SessionStore`, README "Restarts and deploys" |
| `Script`, `Style`, `Preload` in a `PageMeta().Assets` | `go doc via.Assets` |
| `WithTrustedOrigin` in a real `main` | `go doc via.WithTrustedOrigin`, README "Security floor" |
| `List[E]` outside `chat` | `go doc via.List` |
| `WithErrorPage` | `go doc via.WithErrorPage` (`forum` mounts one) |

## Conventions these examples share

- **Listen address:** `cmp.Or(os.Getenv("VIA_ADDR"), ":8080")`, so two examples
  can run side by side without editing code.
- **Serving:** `http.Handle` on the default `ServeMux`, and
  `log.Fatal(http.ListenAndServe(...))` — the error is never dropped.
- **Field export:** via locates fields by offset, so exporting is not required.
  These examples export the reactive fields (`Signal`, `State`, `List`) and keep
  injected dependencies and plain app data unexported.
