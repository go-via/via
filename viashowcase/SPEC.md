# viashowcase — "Signal" build contract

A flagship Via showcase: a **live audience platform** (Slido/Mentimeter-style).
A host creates a *room* (a poll, a word cloud, or a Q&A). The audience joins by
link, and votes/questions stream in real time across a 3-pod cluster with **zero
hand-written JavaScript** — that is the headline demo. It exercises every Via
plugin (picocss, echarts, maplibre), the full state/lifecycle surface, the
NATS-JetStream backplane, Postgres-backed auth + profiles + avatar uploads, and
per-user theme preference.

This file is the AUTHORITATIVE contract. Every builder MUST follow these exact
package names, type names, and signatures so independently-built modules link.
If something here is ambiguous, prefer the lowest-LOC choice and keep it terse.

Module: `github.com/go-via/viashowcase` (own module; `replace` → `../` for via,
`../vianats` for vianats). Go 1.25. Deps: via, vianats, `github.com/jackc/pgx/v5`
(stdlib `database/sql` driver), `nats.go`, `golang.org/x/crypto/bcrypt`, testify.

## Global conventions
- **Lowest LOC possible.** Lean on typed shapes (`Op()`), plugin runtime helpers,
  and `h` control-flow (`h.Each/Switch/When/If`). Small files. No premature
  abstraction. Terse but readable; comments only where intent isn't obvious.
- Brand: amber `#ffbf00`, ink `#0b0b0f`, cream `#efece4`, dark surface `#16181d`.
  picocss default theme `amber`, dark mode default `dark`.
- Errors in actions: return `error`; let Via's handler log it. No panics in
  request paths.
- Every package with pure logic ships `_test.go` covering it; `go test ./...`
  (minus the `e2e` tag) MUST pass with no external services.

## Routes
| Route | Auth | Composition | Purpose |
|---|---|---|---|
| `/` | no | `ui.Home` | landing + (if host) "your rooms" + create-room |
| `/login`, `/signup` | no | `ui.Login`, `ui.Signup` | host auth |
| `/app/profile` | host | `ui.Profile` | display name, avatar upload, theme pref |
| `/r/{code}` | no | `ui.Join` | audience participation (vote / ask / pin) |
| `/host/{code}` | host (owner) | `ui.Host` | big-screen live results: charts + map |
| `GET /avatar/{id}` | no | (HandleFunc) | serve avatar bytes from Postgres |
| `GET /assets/...` | no | (HandleStatic) | embedded branding + custom.css |

Action `Logout` clears the session. Middleware `auth.Require` guards `/app` and
`/host` groups (redirect to `/login`).

## Package layout & responsibilities
```
viashowcase/
  cmd/showcase/main.go        WIRING ONLY (config, db open+migrate, plugins, deps, mount, serve)
  internal/core/              PURE domain — events, folds, codes, theme. FULLY UNIT-TESTED.
  internal/store/             Postgres (database/sql + pgx stdlib). schema.sql embedded.
  internal/auth/              bcrypt + session-backed current-user + middleware.
  internal/ui/                Compositions (UI + actions + lifecycle). Uses core/store/auth.
  internal/assets/            embed.FS: branding svg/png, favicon, custom.css.
  deploy/                     Dockerfile, docker-compose.yml, haproxy.cfg.
  e2e_test.go                 //go:build e2e — compose up, drive cluster, assert convergence.
```

## internal/core  (package `core`) — PURE, deterministic, fully tested
All folds MUST be pure (no clock/RNG/IO/globals), copy-on-write (never mutate
`acc`), and treat unknown variants as no-ops. State is keyed by room `Code`
INSIDE the event, so one global log serves all rooms.

```go
// --- Votes (covers both poll and word-cloud; choices are strings) ---
type Vote struct { Room, Choice, By string }
type Tally  map[string]int            // choice -> count
type Tallies map[string]Tally         // room code -> tally
func (Vote) Fold(acc Tallies, ev Vote) Tallies   // copy; acc[ev.Room][ev.Choice]++
func (Tallies) For(code string) Tally            // nil-safe read
func (Tally) Total() int
func (Tally) Ranked() []Pair                      // sorted desc by count, then Choice asc (deterministic)
type Pair struct { Choice string; Count int }

// --- Q&A ---
type QAEvent struct { Room, Kind, ID, Text, By string }  // Kind: "ask" | "up"
type Question struct { ID, Text, By string; Votes int }
type Boards map[string][]Question                 // room code -> questions
func (QAEvent) Fold(acc Boards, ev QAEvent) Boards // "ask" appends; "up" increments by ID
func (Boards) For(code string) []Question          // returns sorted: Votes desc, then ID asc

// --- Participant pins (maplibre) ---
type Pin struct { Room string; Lng, Lat float64; By string }
type LngLat struct { Lng, Lat float64 }
type PinSets map[string][]LngLat
func (Pin) Fold(acc PinSets, ev Pin) PinSets       // append; cap each room to MaxPins (=500)
func (PinSets) For(code string) []LngLat

// --- Room codes & theme ---
func Code(n int64) string             // deterministic base32-ish short code from n (testable; main feeds a counter/rand seed)
var Themes []string                   // the 19 picocss theme names
func ValidTheme(s string) bool
func ResolveTheme(s string) string    // returns s if valid else "amber"
func ValidMode(s string) string       // "system"|"dark"|"light"; default "dark"
```
`StateAppCounter`-style note: presence ("live now") is NOT in core; it's a
`via.StateApp[map[string]int]` (room→count) mutated via `Update` in the UI layer.

## internal/store (package `store`) — Postgres
`database/sql` with pgx stdlib driver (`_ "github.com/jackc/pgx/v5/stdlib"`,
`sql.Open("pgx", dsn)`). Embed `schema.sql`; `Migrate` execs it (idempotent —
`CREATE TABLE IF NOT EXISTS`).

```go
type Store struct { /* db *sql.DB */ }
func Open(dsn string) (*Store, error)
func (*Store) Migrate(ctx context.Context) error
func (*Store) Close() error

type User struct { ID, Email, Display string }
func (*Store) CreateUser(ctx context.Context, email, passHash, display string) (User, error) // unique email
func (*Store) UserByEmail(ctx context.Context, email string) (u User, passHash string, err error)
func (*Store) UserByID(ctx context.Context, id string) (User, error)

func (*Store) SetAvatar(ctx context.Context, userID, contentType string, data []byte) error
func (*Store) Avatar(ctx context.Context, userID string) (contentType string, data []byte, err error)

func (*Store) SetPref(ctx context.Context, userID, theme, mode string) error
func (*Store) Pref(ctx context.Context, userID string) (theme, mode string, err error)

type Room struct { Code, HostID, Title, Kind string; Choices []string; CreatedAt time.Time }
func (*Store) CreateRoom(ctx context.Context, r Room) error          // Kind: "poll"|"cloud"|"qa"
func (*Store) RoomByCode(ctx context.Context, code string) (Room, error)
func (*Store) RoomsByHost(ctx context.Context, hostID string) ([]Room, error)
```
`ErrNotFound` is an exported sentinel returned when a row is missing.
Schema tables: `users(id pk, email unique, pass_hash, display, created_at)`,
`prefs(user_id pk, theme, mode)`, `avatars(user_id pk, content_type, data bytea)`,
`rooms(code pk, host_id, title, kind, choices text[], created_at)`. IDs are
short strings (use `core.Code` + a counter, or a uuid-ish; keep simple).

## internal/auth (package `auth`)
```go
func Hash(pw string) (string, error)         // bcrypt
func Verify(hash, pw string) bool

type SessionUser struct { ID, Email, Display string }
func Login(ctx *via.Ctx, u SessionUser)      // sess.Put + sess.Rotate
func Logout(ctx *via.Ctx)                     // sess.Clear + Rotate
func Current[S sess.Source](src S) (SessionUser, bool)  // sess.Get; works for *via.Ctx, *via.CtxR, *http.Request
func Require() via.Middleware                 // 302 -> /login if no SessionUser
```
Unit-test `Hash`/`Verify` (roundtrip + wrong-password). Session helpers are thin
wrappers over `sess` — test via build, exercised by ui/e2e.

## internal/ui (package `ui`) — compositions
Dependency injection: a package-level `var Deps struct { DB *store.Store; Map *maplibre.Map; ... }`
set in `main` before Mount (mirrors the pattern Via examples use for pod-local
config). Keep handles minimal. Compositions read `Deps.DB`.

Shared app-global backplane handles, declared field-for-field on each
composition that uses them (Host, Join, and the headless Persistence):
```go
Votes   via.StateAppEvents[core.Vote, core.Tallies]    `via:"votes"`
QA      via.StateAppEvents[core.QAEvent, core.Boards]  `via:"qa"`
Pins    via.StateAppEvents[core.Pin, core.PinSets]     `via:"pins"`
Present via.StateApp[map[string]int]                   `via:"present"`
```
IMPORTANT: do NOT factor these into a shared embedded struct. Via's field walker
(walker.go) only recurses into *child compositions* (types with a `View` method),
not plain embedded structs, so handles inside a non-composition embed are never
app-bound and `Append` silently no-ops. Declaring the same `via:` tags on each
composition is what makes them share one global log; the room `Code` inside each
event discriminates rooms — that is the whole multi-room trick.

Compositions to build (each its own file; keep tiny):
- `layout.go`: `Shell(ctx, title, body)` helper — branding header (wordmark from
  /assets), nav, a **theme picker** (picocss: bind a `<select>` of `core.Themes`
  to `picocss.ThemeRef()` and a dark-mode toggle to `picocss.DarkModeRef()`),
  footer. Used by every page. Also the `<head>` favicon/custom.css are added in
  main via AppendToHead.
- `home.go` (`Home`): landing pitch; if `auth.Current` → list `RoomsByHost` with
  links to `/host/{code}` + share links `/r/{code}`, and a create-room form
  (title, kind via `h.Switch`, choices). Create action writes via `Deps.DB`,
  generates a code, redirects to `/host/{code}`.
- `auth.go` (`Login`, `Signup`): forms; Signup hashes + CreateUser + Login;
  Login verifies + `auth.Login`; redirect to `/`.
- `profile.go` (`Profile`, host-only): edit display name; **avatar upload**
  (`via.File`, save to `Deps.DB.SetAvatar`); **theme + mode** persisted via
  `Deps.DB.SetPref`; show current avatar `<img src="/avatar/{id}">`.
- `room_host.go` (`Host`, `path:"code"`, host+owner): the big-screen view.
  - `OnInit`: load room; if not owner → redirect.
  - `OnConnect`: `Present.Update` +1 for this code; `via.Stream` to push live
    **echarts** updates (Bar of `Votes.For(code).Ranked()` for poll/cloud; a
    questions-over-time or counts view for qa) and refresh the **maplibre** pin
    layer from `Pins.For(code)`.
  - `OnDispose`: `Present.Update` -1.
  - View: title, LIVE watcher count (`Present`), the echarts `Mount()`, the
    maplibre `Mount()`, and for `qa` an upvotable `h.Each` question list.
- `room_join.go` (`Join`, `path:"code"`): the phone view.
  - `OnInit`: load room; capture/prompt nickname (Signal, init random).
  - Vote: `poll`/`cloud` → buttons / text input that `Votes.Append(Vote{code,…})`.
    `qa` → ask box (`QA.Append`(ask)) + upvote buttons (`QA.Append`(up)).
  - Optional "drop a pin" → `Pins.Append`. Use a small maplibre map OR a
    geolocate button.
  - `OnConnect`/`OnDispose`: presence +/- like Host (so the count reflects
    audience too).

Use across the app, at least once each (checklist — do not skip):
`Signal`/`SignalStr`/`SignalBool` (+ `Bind/Text/Show/Op`), `StateTab*`,
`StateSess` (current room/nick), `StateApp`/`StateAppEvents`/`StateAppCounter`,
`OnEvent` (register one consumer in main: persist each Vote to PG via
`Deps.DB`, idempotent by offset — demonstrates durable side-effects),
`on.Click/Key/Submit/Change` (+ `Debounce`), lifecycle `OnInit/OnConnect/OnDispose`,
`path:`/`query:` params, `via.File` upload, `via.Stream`, `Broadcast` (host can
push a "starting now" notice to all room tabs), `h.Each/Switch/When/If/Static`,
all three plugins.

## cmd/showcase/main.go — WIRING ONLY
- Read env: `DATABASE_URL`, `NATS_URL`, `PORT`, `NODE_NAME`.
- `store.Open` + `Migrate` (retry briefly so it tolerates Postgres still booting).
- Backplane: `vianats.JetStream(nc)` if `NATS_URL` set, else `via.InMemory()`.
- `via.New(WithTitle, WithPlugins(picocss(amber,dark) , echarts, maplibre),
  WithBackplane, WithInsecureCookies (demo over http), WithMaxUploadSize)`.
- `AppendToHead` favicon + custom.css; `HandleStatic("/assets/", assets.FS)`;
  `HandleFunc("GET /avatar/{id}", …)` reading `store.Avatar`.
- Set `ui.Deps`. Register the `OnEvent` consumer. `via.Mount` all routes; groups
  with `auth.Require()` for `/app` + `/host`.
- `http.ListenAndServe(":"+PORT, app)`.

## deploy/
- `Dockerfile`: build context = REPO ROOT (`../`), multi-stage, build
  `./viashowcase/cmd/showcase`. (Mirror internal/examples/chatcluster/Dockerfile,
  but module dir is `/src/viashowcase` and build target the cmd path.)
- `docker-compose.yml`: services `postgres` (with healthcheck via `pg_isready`),
  `nats` (`-js -m 8222`, wget healthcheck), three app pods `app1/app2/app3`
  (env: `DATABASE_URL=postgres://via:via@postgres:5432/via?sslmode=disable`,
  `NATS_URL=nats://nats:4222`, `NODE_NAME`, `PORT=3000`), and `lb` (HAProxy
  sticky-cookie on `:3000`, fronting all three). depends_on healthchecks.
  Build context `../..` relative to the compose file? — compose file lives in
  `viashowcase/deploy/`, so `context: ../..` = repo root; `dockerfile:
  viashowcase/deploy/Dockerfile`.
- `haproxy.cfg`: sticky `VIA_LB` cookie, roundrobin over app1/app2/app3, long
  SSE timeouts, docker resolvers + `init-addr last,libc,none`. (Copy the proven
  one from internal/examples/chatcluster, add the third backend.)

## e2e_test.go  (//go:build e2e)
TestMain brings the compose stack up (`up --build -d`), waits for the LB + a
`pg_isready`-backed app readiness, runs tests, tears down. Tests:
1. signup→create poll→vote on one pod converges to the host view on another pod.
2. sticky LB pins one cookie jar to one app across requests.
Use raw `net/http` + cookie jar + regex-scrape of `via_tab` (see
internal/examples/chatcluster/e2e_test.go for the exact action-POST protocol).
