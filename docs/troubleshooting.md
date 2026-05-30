---
title: Troubleshooting
layout: just-the-docs
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

## The tab freezes / actions 404 after a redeploy

**Cause:** a tab's state lives in memory on the server. After a restart, the
client's `via_tab` is unknown to the new process: the next SSE reconnect 404s
and the next action POST 404s. Datastar retries the SSE forever, so the tab
appears frozen rather than erroring.

**Fix:** tell users to reload after a deploy, or drain behind a sticky load
balancer long enough for tabs to close. For session survival across restarts,
persist the `sess.Put` payload to a durable store keyed by the `via_session`
cookie and rehydrate in `OnInit`. See
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

**Cause:** `WithMaxRequestBody(n)` (default 1 MiB) caps action POST and
SSE-close bodies.

**Fix:** raise the limit for routes that accept large uploads:
`via.New(via.WithMaxRequestBody(20 << 20))`.

## State doesn't update across tabs

**Cause:** `StateTab[T]` is per-tab and `StateSess[T]` is per-session.
A second browser tab on the same session shares `StateSess`/`StateApp` but
has its own `StateTab`.

**Fix:** pick the scope you mean — [Reactive state](reactive-state). In tests,
`tc.Fork(path)` opens a second tab on the same cookie jar to exercise
cross-tab `StateSess` behaviour.
