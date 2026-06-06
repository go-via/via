# Signal — the Via flagship showcase

**Signal** is a live audience platform (think Slido/Mentimeter) built to exercise
*every* part of Via in one coherent, production-shaped app. A host creates a
room — a **poll**, a **word cloud**, or a **Q&A** — and shares a link. The
audience joins on their phones and votes/asks; the host's big screen updates the
instant anyone votes, **with zero hand-written JavaScript**, across a 3-pod
cluster.

> This is a demonstration app under `internal`-style `viashowcase/` (its own Go
> module, so Postgres/NATS deps stay out of the core framework). It is the
> reference for "what a real Via app looks like."

## What it demonstrates

| Area | Via mechanism |
|---|---|
| Live poll / word-cloud / Q&A | `StateAppEvents[E,V]` + pure `Fold` (one global log per concern, keyed by room code inside the event) |
| Durable vote history | `OnEvent` consumer → Postgres, idempotent by event offset |
| Live charts | **echarts** plugin (`SetOption`/`SetSeries` pushed over SSE) |
| Participant map | **maplibre** plugin (`SetGeoJSON` pin layer) |
| "● LIVE — N watching" | `StateApp[map[string]int]` mutated in `OnConnect`/`OnDispose` |
| Server push | `via.Stream` ticker |
| Host announcements | `Broadcast` |
| Auth (signup/login) | `sess` + bcrypt + a `Require()` middleware on guarded route groups |
| Profiles | editable **display name**, **avatar upload** (`via.File` → Postgres `bytea`, served at `/avatar/{id}`) |
| **Theme preference** | **picocss** plugin: 19 colour themes + system/dark/light — persists across pages (localStorage, no-flash restore) and per account (Postgres) |
| Phone voting UX | `Signal`/`SignalStr/Num`, `on.Click/Key/Submit/Change`, `Debounce`, `SetSignal` |
| Routing | `path:"code"` room routes, route groups; unguessable (crypto-random) room codes; friendly "room not found" state |
| Accessibility | visible focus rings, reduced-motion support, a screen-reader text alternative for the live chart |
| Reliability / security | graceful shutdown (SSE drain on SIGTERM), `/healthz` DB-ping probe gating the LB + container healthchecks, output-escaped broadcasts |
| Rendering | `h.Switch/Each/When/If`, branded `Shell`, embedded brand assets |

## Architecture

```
            ┌─────────── HAProxy (sticky cookie, :3000) ───────────┐
            │                  │                  │
         app1 (pod)        app2 (pod)        app3 (pod)   ← Via, each its own SSE/tabs
            └───────┬──────────┴──────────┬───────┘
                    │                      │
            NATS JetStream            Postgres
        (StateAppEvents + clustered   (users, prefs, avatars,
         StateApp; cross-pod fan-out)  rooms, durable votes)
```

Per-tab transport (SSE + actions) is pod-local, so the LB is **sticky by cookie**;
the **backplane** converges the shared state across pods. See the framework docs:
[Distributed state](https://go-via.github.io/via/distributed-state).

## Run it (3-pod cluster)

```sh
docker compose -f viashowcase/deploy/docker-compose.yml up --build
open http://localhost:3000
```

Then: **sign up** → **create a poll** → open the share link `/r/{code}` in a
second browser (or your phone) → vote → watch the host big screen move. Use the
theme picker in the header (or the profile page) to recolour — your choice
persists across pages and reloads.

Tear down (also wipes the Postgres volume):

```sh
docker compose -f viashowcase/deploy/docker-compose.yml down -v
```

## Run it as a single node (no infra)

With `NATS_URL`/`DATABASE_URL` unset, the backplane falls back to in-process
`via.InMemory()`. A Postgres URL is still required for auth/profiles:

```sh
DATABASE_URL=postgres://via:via@localhost:5432/via?sslmode=disable \
  go run ./cmd/showcase
```

## Layout

```
viashowcase/
  cmd/showcase/main.go     wiring: config, db, plugins, OnEvent consumer, routes, serve
  internal/core/           pure domain — folds (votes/qa/pins), codes, theme (unit-tested)
  internal/store/          Postgres — users, prefs, avatars, rooms, votes (+ embedded schema)
  internal/auth/           bcrypt + session-backed current-user + Require() middleware
  internal/ui/             compositions — layout, home, auth, profile, room_host, room_join
  internal/assets/         embedded Via branding + brand-palette custom.css
  deploy/                  Dockerfile · 3-pod docker-compose · sticky HAProxy
  e2e_test.go              //go:build e2e — full-cluster convergence + sticky-LB tests
  SPEC.md                  the build contract / design notes
```

## Tests

```sh
go test ./...                         # core + auth unit tests (no infra needed)
go test -tags e2e -timeout 15m .      # full docker-compose cluster e2e (needs Docker)
```

## Environment

| Env | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | _(required)_ | Postgres DSN for auth/profiles/rooms |
| `NATS_URL` | _(unset → InMemory)_ | NATS URL; enables the durable JetStream backplane |
| `PORT` | `3000` | HTTP listen port |
| `NODE_NAME` | `node` | Pod identity (logs) |
