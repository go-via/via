# Examples

Run any of them with `go run ./example/<name>`.

| example | what it shows |
| --- | --- |
| `counter` | server-rendered state, one action, no client signal |
| `greeting` | a client-resident `Signal`, two-way bound and displayed live |
| `pulse` | a server tick pushed to one tab over SSE |
| `feed` | a `Topic` broadcast fanning out to every connected tab |
| `poll` | a reordering list where `via.OnArg` carries each row's id |
| `dashboard` | several independent live regions on one SSE stream |
| `chat` | multi-user chat with a presence count |
| `forum` | a multi-page app: router, sessions, `PostForm` + `Redirect`, upload |

## Conventions these examples share

- **Listen address:** `cmp.Or(os.Getenv("VIA_ADDR"), ":8080")`, so two examples
  can run side by side without editing code.
- **Serving:** `http.Handle` on the default `ServeMux`, and
  `log.Fatal(http.ListenAndServe(...))` — the error is never dropped.
- **Field export:** via locates fields by offset, so exporting is not required.
  These examples export the reactive fields (`Signal`, `State`, `List`) and keep
  injected dependencies and plain app data unexported.
