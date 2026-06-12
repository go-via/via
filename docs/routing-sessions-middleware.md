---
title: Routing, sessions & middleware
layout: default
parent: Guides
nav_order: 4
---

# Routing, sessions & middleware
{: .no_toc }

1. TOC
{:toc}

## Routing and groups

```go
via.Mount[Counter](app, "/counter/{id}")

api := app.Group("/api")
api.Use(requireAuth)
via.Mount[Profile](api, "/profile")

api.HandleFunc("POST /widgets", createWidget) // method-prefixed
api.HandleFunc("/widgets",       listWidgets) // bare path = GET

app.Routes()                                  // []RouteInfo for boot logging
```

Group patterns follow the `http.ServeMux` shape: `"GET /foo"`, `"POST /foo"`,
or just `"/foo"` (defaults to GET; an unrecognised method token is treated
as a path). Mounting two routes at the same path panics at registration with
the offending pattern and the original registrar tag. `WithNotFound(h)`
installs a custom 404 handler.

## Path parameters

```go
type Profile struct {
    UserID int    `path:"id"`
    Slug   string `path:"slug"`
}
via.Mount[Profile](app, "/u/{id}/posts/{slug}")
```

Each `path:"name"` tag must match a `{name}` segment. Reflection runs once at
Mount; per-request decoding writes directly into the typed field. Query
parameters decode the same way via the `query:"name"` tag.

## Sessions

Per-browser session storage, keyed by Go type, lives in `via/sess`:

```go
import "github.com/go-via/via/sess"

type User struct{ Email, Name string }

sess.Put(ctx, User{Email: "alice@example.com", Name: "Alice"})
u, ok := sess.Get[User](ctx)                 // handler / action
u, ok := sess.Get[User](r)                   // middleware (*http.Request)
sess.Clear[User](ctx)
sess.Rotate(ctx)                             // after login / privilege change
```

`sess.Rotate` issues a fresh session id and copies the data across — call it
after any auth-state change to defend against session fixation.

`requireAuth` is one line of middleware:

```go
func requireAuth(w http.ResponseWriter, r *http.Request, next http.Handler) {
    if u, ok := sess.Get[User](r); !ok || u.Email == "" {
        http.Redirect(w, r, "/login", http.StatusSeeOther)
        return
    }
    next.ServeHTTP(w, r)
}
```

{: .warning }
Sessions are in-memory and do not survive a process restart. To persist
across restarts, store the `sess.Put` payload in a durable store keyed by
the `via_session` cookie and rehydrate in `OnInit`.

## Middleware

```go
import "github.com/go-via/via/mw"

app := via.New()
mw.Defaults(app)                // RequestID + AccessLog + Recover
app.Use(mw.CSP())               // CSP with per-request nonce
app.Use(requireAuth)            // your own
```

Factories under `via/mw`:

- `mw.Defaults(app)` — RequestID + AccessLog + Recover.
- `mw.RequestID()` — stamp `X-Request-ID` + plant on `r.Context`.
- `mw.AccessLog(app)` — one info-line per request, with rid + status; CR/LF
  stripped from method/path/rid so user input can't forge log entries
  (CWE-117).
- `mw.Recover(app)` — panic → 500 + error log (same CR/LF scrub); the
  goroutine survives.
- `mw.CSP(extra…)` — CSP header + nonce on `r.Context`; includes
  `'unsafe-eval'`, which the bundled runtime requires (see
  [production](production)).
- `mw.HSTS(opts…)` — Strict-Transport-Security for HTTPS deploys.
- `mw.RedirectHTTPS()` — 301 plain HTTP → https; trusts `X-Forwarded-Proto`
  (use behind a TLS-terminating proxy).
- `mw.RedirectHTTPSStrict()` — same redirect but ignores XFP; only
  `r.TLS != nil` counts as secure (use for direct-bind TLS).

Read middleware output back inside actions / handlers:

```go
via.RequestIDFrom(r)             // string or ""
via.Log(ctx).Log(via.LogInfo, "checkout", "amount", n)
ctx.CSPNonce()                   // matches header set by mw.CSP
```
