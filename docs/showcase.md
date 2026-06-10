---
title: Showcase — Signal
layout: default
nav_order: 7
---

# Showcase: Signal
{: .no_toc }

1. TOC
{:toc}

**Signal** is the flagship Via application — a live audience platform
(Slido/Mentimeter-style) built to exercise *every* part of the framework in one
coherent, production-shaped app. A host creates a room — a **poll**, a **word
cloud**, or a **Q&A** — and shares a link. The audience joins on their phones and
votes; the host's big screen updates the instant anyone votes, **with zero
hand-written JavaScript**, across a three-pod cluster.

It lives in the repo at [`viashowcase/`](https://github.com/go-via/via/tree/main/viashowcase)
as its own Go module (so Postgres/NATS dependencies stay out of the core
framework). Treat it as the reference for "what a real Via app looks like."

## The thesis it proves

The phone vote → moving bar chart, with no client JS and across separate server
pods, is the whole pitch in one gesture: the client/server reactive split is a
typed field, the transport is SSE, and shared state converges over a backplane.

## Every mechanism, in one app

| Feature | Via mechanism |
|---|---|
| Live poll / word-cloud / Q&A | `StateAppEvents[E,V]` + a pure `Fold` |
| Durable vote history | `OnEvent` consumer → Postgres, idempotent by event offset |
| Live result charts | [echarts plugin](plugins) — `SetOption`/`SetSeries` over SSE |
| Participant map | [maplibre plugin](plugins) — a GeoJSON pin layer |
| "● LIVE — N watching" | clustered `StateApp[map[string]int]`, bumped in `OnConnect`/`OnDispose` |
| Server push | `via.Stream` ticker |
| Host announcements | `Broadcast` |
| Auth (signup / login) | `sess` + bcrypt + a `Require()` middleware on guarded route groups |
| Profiles + avatar upload | `via.File` → Postgres `bytea`, served at `/avatar/{id}` |
| Theme preference | [picocss plugin](plugins) — 19 colour themes + system/dark/light, persisted per user |
| Phone voting UX | `Signal`/`SignalStr`, `on.Click/Key/Submit/Change`, `Debounce`, `SetSignal` |
| Routing | `path:"code"` room routes + route groups |
| Rendering | `h.Switch/Each/When/If`, a branded `Shell`, embedded brand assets |

See [Reactive state](reactive-state), [Distributed state](distributed-state),
and [Plugins](plugins) for the mechanisms themselves.

## Multi-room over a single log

Via wire keys are static (bound once at mount), so there is no per-instance key.
Signal gets **one room per code** anyway by keying the room *inside the event*:
a single app-global `StateAppEvents` log per concern (votes, Q&A, pins), whose
`Fold` maintains a `map[code]→state`. Each composition selects its room with a
`path:"code"` field. This is the idiomatic way to model per-entity state on a
shared log.

```go
type Vote struct{ Room, Choice, By string }
type Tally map[string]int       // choice -> count
type Tallies map[string]Tally   // room code -> tally

func (Vote) Fold(acc Tallies, ev Vote) Tallies { /* copy; acc[ev.Room][ev.Choice]++ */ }
```

{: .note }
The handles must be **direct fields** of each composition — Via's field walker
binds handles inside child compositions, not inside a plain embedded struct, so a
state-only embed would leave `Append` an unbound no-op. Declaring the same
`via:` tags on each composition is exactly what makes them share one log.

## Architecture

```
            ┌─────────── HAProxy (sticky cookie, :3000) ───────────┐
            │                  │                  │
         app1 (pod)        app2 (pod)        app3 (pod)
            └───────┬──────────┴──────────┬───────┘
                    │                      │
            NATS JetStream            Postgres
        (StateAppEvents + clustered   (users, prefs, avatars,
         StateApp; cross-pod fan-out)  rooms, durable votes)
```

A tab's SSE stream and its action POSTs are pod-local, so the load balancer is
**sticky by cookie**; the backplane converges the shared state across pods. This
is the supported way to scale Via horizontally today — affinity for transport,
backplane for state ([Distributed state](distributed-state)).

## Run it

```sh
docker compose -f viashowcase/deploy/docker-compose.yml up --build
# open http://localhost:3000
```

Sign up → create a poll → open the share link `/r/{code}` in a second browser or
on your phone → vote → watch the host big screen move. The profile page changes
the colour theme and dark/light mode, persisted to Postgres.

Tear down (also wipes the database volume):

```sh
docker compose -f viashowcase/deploy/docker-compose.yml down -v
```

The full source, layout, and build contract are in
[`viashowcase/`](https://github.com/go-via/via/tree/main/viashowcase) — see its
`README.md` and `SPEC.md`.
