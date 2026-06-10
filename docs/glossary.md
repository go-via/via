---
title: Glossary
layout: default
parent: Reference & ops
nav_order: 5
---

# Glossary

Short definitions for the vocabulary used across these docs.

**Composition** — a Go struct that defines a piece of UI: reactive state as
typed fields, actions as methods, and a `View`. Mounted at a route with
`via.Mount[T]`, and nested as a field in another composition — see
[Compositions](compositions).

**Signal (`Signal[T]`)** — client-owned reactive state, mirrored into the
browser by Datastar. Bind it to inputs and view helpers; it reacts
client-side with no round-trip.

**State (`StateTab[T]` / `StateSess[T]` / `StateApp[T]`)** — server-owned
reactive state at per-tab, per-session, and global scope respectively. Lives
only in Go; changes flow to the client through a re-render. See
[Reactive state](reactive-state).

**Datastar** — the browser runtime Via targets; it keeps the page reactive and
updates it in place (a morph, not a full reload). `Signal[T]` values
live as client signals it manages, and `data-*` attributes are their
subscriptions. Via emits the `data-*` attributes and SSE patches that drive it;
you don't write Datastar by hand.

**Action** — a composition method (`func(*via.Ctx) error` or
`func(*via.Ctx)`) bound to a DOM event with the `on` package. Runs
server-side; per-tab actions are serialized by the action mutex.

**View / render** — the `View(ctx *via.CtxR) h.H` method that produces the
page's HTML tree. Read-only: it can read state but not mutate it.

**Flush** — the moment after an action (or `via.Stream` callback) when Via
diffs the View against the previous emission and ships the resulting
element/attribute patches plus signal deltas over SSE.

**Dirty-mark** — the internal flag set when a write changes state, telling
the next flush that a re-render and patch are needed. `ctx.SyncOff()` opts an
action out of this cycle.

**Morph** — applying a DOM patch in place rather than replacing nodes, so
focus, scroll, and element identity are preserved. `h.DataIgnoreMorph()`
opts an element out.

**Action mutex** — the per-tab lock that serializes action handlers (and
`SyncNow`) so concurrent POSTs to one tab cannot race on state writes.

**`via_tab` / `via_session`** — the per-tab id (also the CSRF token) and the
session cookie. Unknown `via_tab` ids 404; the session cookie is `HttpOnly`,
`SameSite=Lax`, and `Secure` by default. See
[Production & ops](production#security-defaults).

**SSE (Server-Sent Events)** — the one-way server→client stream Via uses for
all live updates: one stream per tab, no WebSockets.

**Ticker (`via.Ticker`)** — the handle returned by `via.Stream` to pause,
resume, stop, or re-time a server-push loop.
