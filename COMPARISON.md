# via vs via-v2 — Architect's Decision Report

An expert-panel comparison of the original via framework (`main`, ~24k core LOC)
to this bare-core reimagining (`v2`, ~1.4k wiring core / ~2.3k incl. test
harnesses). Seven domain experts read both codebases; every comparison was
adversarially fact-checked against the actual source before synthesis.

## 1. Verdict

**v2 is the right direction for the core, but it is not yet a replacement for
main.** It trades breadth and distribution for a dramatically smaller, safer,
lint-provable wiring substrate: reflection-free generics binding (CI-enforced
over core files), a single-goroutine live-island model that kills whole race
classes by construction, secure-by-default CSP + a fail-closed origin floor, and
95%+ core coverage at a ~1:1 test ratio. What it fundamentally trades away is
**scope**: no session/app-scoped or cross-pod state, no router/groups, no
middleware, no file uploads, no plugins, and — most acutely — **no resilience
layer at all** (no keepalive, no at-least-once delivery, no reconnect). The trade
is worth making *as a rebuild of the foundation*, because main's expressiveness
rests on reflective machinery (PC-trampoline `-fm` parsing guarded by a boot
canary) that v2 has shown is unnecessary. But two gaps are load-bearing and
non-negotiable before v2 can host a real app: **server/client reconnect +
half-open detection**, and **session-scoped state**. Confidence: high — every
dimension was fact-checked and the load-bearing claims survived.

## 2. Comparison Matrix

| Dimension | main | v2 | Edge |
|---|---|---|---|
| **Wiring & architecture** | Reflection-built descriptor; name-stable action identity survives View restructuring & lists; nested composition tree; declarative tag-driven inputs (path/query/file/scopes) | Reflection-free generics + positional action ids; compile-time binding errors; no composition tree; single root viewer | **tie** — v2 safer/simpler; main more expressive at scale |
| **Reactive & state model** | 4-quadrant taxonomy; cross-pod StateSess/StateApp via CAS backplane; fine-grained read-tracked fan-out; rejectable `Update(fn) error` | 4-type model (Signal/Local/State/List); single-goroutine island mutation; principled signal-patch vs element-patch split; per-connection only, last-write-wins | **main** — only main does shared/persistent/cross-pod reactive state |
| **Live / SSE / resilience** | Keepalive half-open detection, at-least-once drain queue, server re-bootstrap + client reconnect banner, write deadlines, real cross-pod fan-out | One SSE stream per tab, lock-free pulse channel, clean 410 on closed tab, correct multi-line framing — but **no** keepalive/deadline/reconnect/redelivery; write errors discarded | **main** — decisively; v2 deferred nearly all resilience |
| **Feature surface & gaps** | Routing + typed path params, Groups + middleware, HMAC sessions, multipart uploads, durable state, 3 plugins, showcase app | Single root at `/{$}`, live islands, in-process `topic.Topic`, per-request CSP+nonce, Each/If/When helpers | **main** — ships ~6 subsystems v2 entirely lacks |
| **Security & CSRF** | Cookie + via_tab two-factor (256-bit), session Rotate (fixation defense), correct CSP nonce reuse on push — **but** CSP opt-in only, and session gate is *conditional* on a bound session | CSP + nosniff on by default, fail-closed origin floor (independent of tab secrecy), cookieless (no clobber class), 1 MiB body cap; tab id 128-bit, live-only | **tie** — v2 sounder-by-default; main more defense-in-depth *when configured* |
| **Testing & code health** | vt harness self-tested (89%), typed `Action(p.Method)` addressing, 130 files / 21 pkgs breadth | Reflection-free *enforced* by AST tests, novel no-&/no-closure invariants, 95.4% core / 100% topic; vt harness 0% self-coverage, integer `Action(n)` brittle | **v2** — testability-per-LOC + enforced invariants; main wins breadth |
| **Prod readiness & ecosystem** | NATS JetStream backplane + conformance suite, graceful drain, livez/healthz/readyz, 3-pod cluster (HAProxy+NATS+Postgres), reconnect IIFE, plugins | Honest frozen single-pod core; in-process topic only; uncapped + origin-*unchecked* SSE GET stream; no probes, no reconnect | **main** — deployable at horizontal scale today |

Fact-check-driven corrections folded into the matrix: v2's positional-id 410 is
**stateless-path only** (live path silently no-ops + re-syncs via SSE); v2 is
lock-free *on state mutation* (registry map still takes a mutex); main's session
gate is *conditional* on a bound session, not uniform; in the showcase Postgres
is **app-side persistence**, not a backplane backend (NATS is the only
implemented backplane); v2's reflect/closure bans are real but **scoped**
(core-file import scan; example-only closure lint, self-described interim).

## 3. What v2 Must Regain to Replace main

Prioritized by how load-bearing the gap is. "Within guarantees" = achievable
without breaking no-reflection / no-identifier-strings / no-closure-at-call-sites.

| # | Gap | Effort | Within guarantees? |
|---|---|---|---|
| 1 | **Resilience floor: keepalive/heartbeat + write deadlines** so a dead peer doesn't leak the island goroutine + ticker, and a stalled client can't pin the single goroutine. | **S** | Yes — pure server-loop plumbing, no wiring impact. |
| 2 | **Server reconnect/re-bootstrap + client reconnect banner.** main's `reconnect.go` IIFE is portable as-is; covers Datastar's clean-close no-retry freeze. v2's own ROADMAP names this a pending floor. | **M** | Yes — client IIFE + server resume handshake; no reflection/identifier-string needed. |
| 3 | **Session-scoped state** (signed cookie + typed per-user store, `Rotate`-equivalent for fixation defense). Single largest gap for any authed app; cookieless currently pushes this entirely onto app authors. | **L** | Mostly — store/cookie need no reflection. Tension: scoped state historically rode struct-tag declaration; v2 must express scope via handles/generics, not `via:"..."` tags. |
| 4 | **At-least-once (or buffered) delivery** so a push onto a dropping socket isn't silently lost (v2 currently discards the write error too). | **M** | Yes — a per-connection redelivery queue, server-internal. |
| 5 | **Name- or structural-path action identity** so bindings survive View branching and **lists-with-actions** (today positional ids + `shapeMatches` 410 on the stateless path; live path no-ops out-of-range + re-syncs; row-level actions explicitly punted). | **L** | **Compromise risk** — name stability historically came from reflection. v2 must invent a structural-path cursor that stays reflection-free and identifier-string-free; the hardest "within guarantees" item. |
| 6 | **Router: multi-page Mount + Groups + middleware pipeline + typed path params.** Today only `/{$}` + internal action paths; no middleware abstraction at all. | **L** | Partly — routing/middleware are reflection-free; typed path-param *binding* leaned on tags in main, needs a handle-based decode. |
| 7 | **Cross-pod / durable backplane** — or an explicit, documented sticky-session-only scaling stance. Today multi-pod shared state is impossible (`topic` is in-process). | **L** | Yes — pluggable interface behind `topic`; orthogonal to wiring guarantees. |
| 8 | **Cap + origin-check the SSE GET stream** before any internet-facing deploy (currently uncapped and, unlike the POST path, *not* origin-checked). | **S** | Yes — add the existing `originAllowed` floor to the GET handler + a max-connections cap. |
| 9 | **Multipart file-upload binding** into typed component fields. | **M** | Compromise risk — main bound files via reflective struct fields; v2 needs an explicit handle API. |
| 10 | **Plugin / asset-embedding story** (echarts/maplibre/picocss-class) + one production-shaped reference deployment. | **L** | Yes — embedding is independent of wiring guarantees. |
| 11 | **Richer reactive/binding surface**: Computed/Effect, Show/Class/Attr/Style helpers, wider `on.*` vocab (Debounce/Throttle/Key/Confirm/Indicator), numeric Clamp/AtLeast/AtMost across scopes. | **M** | Yes — helper APIs, no reflection needed. |
| 12 | **vt harness self-coverage + compile-safe action addressing in tests** (integer `Action(n)` is brittle; renumbering silently breaks intent with no compiler help). | **M** | Coupled to #5 — typed addressing returns once action identity is non-positional. |

## 4. What v2 Got Genuinely Right (main should envy)

- **Reflection-free wiring, structurally locked.** Zero `reflect` in core,
  CI-enforced via an AST import-scan test. Eliminates main's fragile PC-trampoline
  `-fm` name parsing and the boot canary that exists *only* to detect when a Go
  upgrade breaks that contract. The single most solid, fully-verified win.
- **Single-goroutine island model.** Mutation funnels through one unbuffered
  pulse channel — no per-key mutex, no CAS retry loop, no monotone-rev gate.
  (Precise: the *only* lock is on the connection-registry map; state mutation
  itself is lock-free.) Kills entire race classes by construction.
- **Principled signal-patch vs element-patch split.** Server-driven signal
  writes ride a dedicated frame from `ctx.dirty` and never clobber a field the
  user is in-flight typing into — main re-renders the whole fragment with no
  equivalent separation. The browser tier even regression-tests
  focus-preservation-during-typing, the exact bug class main's own history hit.
- **Secure-by-default posture.** CSP + nosniff on every render (main ships none
  unless `mw.CSP()` is remembered), plus an independent fail-closed origin floor
  that doesn't depend on tab-id secrecy. Cookieless also sheds main's
  two-apps-on-one-host session-clobber 403-freeze class entirely.
- **Executable design invariants.** AST tests that *ban* `&`/closures at call
  sites and ban `reflect`. (Honest scope: the no-&/no-closure lint runs over
  examples only and is self-described "interim" pending a type-level ban; the
  reflect ban is a core-file import scan. Real but narrower than headline framing.)
- **Honest contract.** No half-built distribution to operate or mistrust; the
  experimental-backplane churn risk simply doesn't exist.

## 5. Recommendation

**Coexist now, with v2 as the designated successor core — do not replace yet.**
v2 has proven the foundational thesis (reflection-free, race-free,
secure-by-default) is achievable and superior; main retains every load-bearing
production subsystem v2 lacks. Cherry-picking *from* v2 into main is not
worthwhile — the wins are architectural, not portable patches.

**Single concrete next move:** land the **resilience floor (#1) + reconnect
(#2)** on v2 first. They are S+M effort, fully within v2's guarantees, and are
the gating blocker — without half-open detection and reconnect, v2 cannot host
*any* long-lived session regardless of what else it grows. Port main's
`reconnect.go` IIFE verbatim as the client floor (v2's own ROADMAP already calls
for this) and add a keepalive ticker + write deadline to `runLiveStream`.
Sequence after that: SSE origin-check/cap (#8, trivial), then session-scoped
state (#3) as the first real feature gap. Defer the action-identity rework (#5)
and router (#6) until a concrete multi-page/lists-with-actions app forces the
design — that is where the no-reflection guarantee will be genuinely tested.
