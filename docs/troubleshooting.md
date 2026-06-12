---
title: Troubleshooting
layout: default
parent: Reference & ops
nav_order: 4
---

# Troubleshooting
{: .no_toc }

Symptom → cause → fix for the things that trip people up first.

1. TOC
{:toc}

## Nothing happens on `http://localhost` — actions don't fire

**Cause:** the `via_session` cookie is `Secure` by default, so the browser
won't send it over plain `http://`. Without the cookie the action POST is
session-mismatched.

**Fix:** opt out of `Secure` for local development only:

```go
app := via.New(via.WithInsecureCookies())
```

Never ship `WithInsecureCookies()` to production — it drops the `Secure` flag.

## `EvalError: Refused to evaluate a string as JavaScript`

**Cause:** a Content-Security-Policy whose `script-src` lacks
`'unsafe-eval'`. Via's bundled Datastar runtime compiles every
`data-*` expression and event handler with `Function()`, which CSP
gates behind that keyword — without it every click and binding throws
this error.

**Fix:** use `mw.CSP()`, whose default policy includes
`'unsafe-eval'`, or add the keyword to your own policy's
`script-src`. The keyword is bounded here: it authorizes eval only
inside script the policy already admitted (same-origin files and
nonce-carrying tags); inline script injection stays nonce-gated.

## The tab shows stale state / resets after a redeploy

**Cause:** a tab's state lives in memory on the server and does not survive a
restart. When the SSE stream reconnects with a `via_tab` the new process
doesn't know, via re-bootstraps the tab in place: a fresh `*via.Ctx` is built
from the route and the `Referer`'s path/query params, `OnInit` runs again, and
the full view plus a new signal seed replace the stale UI. When the page
request can't be reconstructed (a path-param route with no usable `Referer`),
via pushes an explicit `window.location.reload()` instead. Either way the tab
recovers without user action — but in-memory tab state starts over.

**Fix:** for state that must survive a deploy, persist the `sess.Put` payload
to a durable store keyed by the `via_session` cookie and rehydrate in `OnInit`
(which also runs on every re-bootstrap). Watch `via.sse.recover` to see how
often clients hit this path. See
[Production & ops](production#restart-and-tab-survivability).

## `on.Click(c.DoThing)` won't compile

**Cause:** `DoThing` isn't a valid action. An action must be a method on the
composition with signature `func(*via.Ctx) error` or `func(*via.Ctx)`.

**Fix:** check the receiver and signature. The typed `on.Click(c.DoThing)`
form is deliberately strict — a typo or wrong signature is a compile error
(that's the feature). The string form `on.Click("DoThing")` also exists.

## `OnConnect` never runs

**Cause:** `OnConnect` fires the first time the **SSE stream** opens, not on
the page GET. A crawler or a `curl` that fetches HTML without opening the SSE
never triggers it.

**Fix:** that's intended — put cheap setup in `OnInit` (runs on the GET) and
expensive per-tab work (tickers, fan-out goroutines) in `OnConnect`.

## A `Mount` call panics at startup

**Cause:** registration-time errors panic by design — e.g. two routes at the
same path, a `path:"name"` tag with no matching `{name}` segment, or a
composition with no `View` method.

**Fix:** read the panic message; it names the offending pattern and the
registrar. Registration mistakes are programming errors, so they fail loudly
at boot rather than at request time.

## An oversized upload / POST returns 413

**Cause:** two separate caps apply. `WithMaxRequestBody(n)` (default 1 MiB)
caps plain action POST and SSE-close bodies; `WithMaxUploadSize(n)` (default
32 MiB) caps `multipart/form-data` bodies.

**Fix:** raise the matching limit — `via.New(via.WithMaxUploadSize(64 << 20))`
for file uploads, `WithMaxRequestBody` for large JSON actions. Use
`WithRequestTooLarge(h)` to customise the 413 response.

## State doesn't update across tabs

**Cause:** `StateTab[T]` is per-tab and `StateSess[T]` is per-session.
A second browser tab on the same session shares `StateSess`/`StateApp` but
has its own `StateTab`.

**Fix:** pick the scope you mean — [Reactive state](reactive-state). In tests,
`tc.Fork(path)` opens a second tab on the same cookie jar to exercise
cross-tab `StateSess` behaviour.
