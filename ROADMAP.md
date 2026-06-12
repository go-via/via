# Via Roadmap to 10/10 (ratified)

## Preamble

Where we are (final panel verdict, averages of five lenses): 1.0
readiness 3.6, Plugin system 3.8, Backplane 4.2, Broadcast 4.6, on.*
5.4, Ease of use 5.4, File uploads 5.4, Sessions 5.6, Docs 5.6, Typed
shapes 5.8, Overall DX 5.8, SSE 6.2, Composition 6.2, Testing harness
6.2, h DSL 6.4, Actions 6.4, Config/ops 6.4, Reactive state 6.6. The
panel certified 13 consensus release blockers, including a RED test
suite on clean checkout, a recommended CSP that bricks the runtime,
silently dying backplane tailers, and an unkeepable durability promise.

The goal: every scored feature at 9-10, a defensible v1.0.0, and —
decisively — gates that make each fix structurally unregressable.
10/10 is the gates staying green, not the features landing.

Two corpus-wide rules bind every item below:

- Same-PR docs rule (E): any PR that changes a default, behavior, or
  public API ships its doc correction and its MIGRATION.md anchor in
  the same PR. Long-form guides may lag; lies may not.
- Proof-marker convention (E, replacing D6's judgment-call wording):
  every durability or safety claim in docs carries
  `<!-- proof: TestName -->`. CI verifies each marker names an
  existing in-tree test and that every paragraph in the claim
  registry file carries a marker. A safety sentence may only appear
  (or return) in the same PR as its named green test.

Verify clauses follow CONVENTIONS.md: test-first, behavioral names
(`TestSubject_doesWhat`), outside-in through exported API, real over
stub over mock, table-driven where parameterized, `go test -race`.

## P0 — Stop the bleeding, stop the lying

Phase goal: the tree stops being wrong. Every defect here either ships
incorrect behavior on a clean checkout (RED suite, inverted math,
bricked CSP, lost frames, dead tailers, diverging projections) or
asserts something in print a user can disprove in an afternoon. No new
public API ships this phase except the vtbrowser module and the two
docs gates. Verification infrastructure (browser harness, snippet
gate, migration gate, proof markers) lands first so every later claim
is checkable.

Exit criteria:

- `go test -race ./...` exits 0 on a network-isolated clean checkout.
- `./ci-check.sh --browser` green, including
  `TestBrowser_clickFiresUnderDefaultCSPPolicy` with zero console
  errors — first proof the recommended config runs the runtime.
- E5 snippet gate green; MIGRATION.md exists with the A1 entry; the
  apidiff-to-MIGRATION coupling job exits 0.
- Proof-marker gate live: every marker names an existing test; every
  registry-listed paragraph carries a marker.
- `compaction_gap_halt` fires in the overtake drill scenario; all four
  tailers survive an injected channel close with `tailer_up` back to 1.

### P0.1 — Headless browser CI harness (vtbrowser)

- Origin: B1.
- Change: new `/vtbrowser` submodule with its own go.mod so chromedp
  never taints the dep-free core. API mirrors vt: `Open(t, app)`,
  `Click`, `Type`, `WaitText`, `ConsoleErrors`. `ci-check.sh
  --browser` job, skipped locally without Chromium, mandatory in CI.
  Milestones prove what vt structurally cannot: morph focus/scroll
  preservation, expression eval, reconnect banner transitions.
- Verify: `TestBrowser_clickIncrementsCounter`,
  `TestBrowser_morphPreservesFocusedInputAcrossPatch`,
  `TestBrowser_reconnectBannerClearsOnResume`; every browser test
  asserts `ConsoleErrors()` empty; core go.mod byte-identical
  before/after (A's amendment).
- Lifts: vt 6.2→8; verification substrate for all client claims.
- Deps: none.
- Size: L.

### P0.2 — Docs gates: snippet-compile CI plus MIGRATION.md coupling

- Origin: E5, E20 (A19 snippet sub-item absorbed).
- Change: `internal/docstest` extractor lifts every Go fence in
  docs/*.md and README.md, wraps via per-page preamble comments,
  compiles against the local module (compile-only; opt-out requires a
  reason). New MIGRATION.md; a CI job diffs the apidiff report against
  MIGRATION.md anchors so no public break lands undocumented.
  Mechanism choice: E5's in-place extractor over include-files and
  Example funcs (chair ruling 8).
- Verify: `TestDocstest_failsOnUndeclaredIdentifier`,
  `TestMigration_coversEveryAPIDiffBreak`; both jobs exit 0 on tree;
  every MIGRATION anchor's before/after snippet compiles.
- Lifts: Docs 5.6→7; 1.0 3.6→4.5.
- Deps: none (P0.3 needs it in the same release).
- Size: L.

### P0.3 — Replace inverted NumOps.Min/Max with AtLeast/AtMost/Clamp

- Origin: A1, E's amendment.
- Change: shape_num.go — delete `Min`/`Max` (semantics inverted vs
  `math.Min` intuition); add `AtLeast(lo)`, `AtMost(hi)`, `Clamp(lo,
  hi)`; `Clamp` panics on inverted bounds. No deprecation alias
  pre-1.0, but only with a same-PR MIGRATION.md entry and a sweep of
  every doc/example mentioning Min/Max (E's amendment).
- Verify: `TestNumOps_atLeastRaisesValueBelowFloor`,
  `TestNumOps_atMostLowersValueAboveCeiling`,
  `TestNumOps_clampPanicsOnInvertedBounds`; grep shows no `Min`/`Max`
  on NumOps; apidiff records the removal against the MIGRATION anchor.
- Lifts: Shapes 5.8→7.
- Deps: P0.2.
- Size: S.

### P0.4 — Embed plugin assets by default; SRI-mandatory CDN opt-in

- Origin: A2, A10, B8, C1, C4 (five-way unanimous merge).
- Change: picocss/echarts/maplibre vendor pinned assets via
  `go:embed`, served at content-hashed `/via/assets/...` paths with
  immutable cache headers (C1). Delete the boot-time jsDelivr fetch
  and the pico.go:321 panic. `WithThemes`/`WithDefaultTheme` conflict
  panics (A2). CDN is opt-in only: `WithCDN(url, integrity)` panics at
  registration on empty or malformed SRI (must parse
  sha256/384/512-base64, C4); a version bump without a new hash panics
  (B8). `go:generate` dev-time refresh scripts.
- Verify: both blocker halves (C's amendment):
  `TestPlugin_registersWithoutNetworkAccess` (net.Dialer fails all
  dials), `TestPicoPlugin_registersWithoutNetwork`,
  `TestPlugin_panicsOnCDNSourceWithoutIntegrity`,
  `TestPlugin_emitsIntegrityAndCrossoriginForCDN`,
  `TestPage_rendersNoThirdPartyScriptWithoutIntegrity`;
  `go test -race ./...` green under `unshare -n`.
- Lifts: Plugin 3.8→6.5; 1.0 +1; kills blockers 3 and 4.
- Deps: none.
- Size: M.

### P0.5 — Honest default CSP with browser proof

- Origin: B6 + C2 merged (per settled vote); E7's doc half.
- Change: default `mw.CSP()` emits `default-src 'self'; script-src
  'self' 'nonce-X' 'unsafe-eval'; object-src 'none'; base-uri 'self';
  frame-ancestors 'self'`. Godoc why-paragraph (eval authorizes
  `Function()` on already-admitted same-origin script; it does not
  re-enable inline injection), troubleshooting entry for `EvalError:
  Refused to evaluate`, and the production.md CSP section land in the
  same PR. The name `CSPStrict` is reserved until P3.2 makes it true;
  the interim CSPStrict is cut (unanimous).
- Verify: `TestCSP_defaultPolicyIncludesUnsafeEval`,
  `TestCSP_setsFrameAncestorsSelf`,
  `TestBrowser_clickFiresUnderDefaultCSPPolicy` (default `mw.CSP()`,
  click increments, zero console errors);
  `grep -r "unsafe-eval" docs/` non-empty.
- Lifts: Config/ops 6.4→7.5; kills blocker 1.
- Deps: P0.1.
- Size: S.

### P0.6 — Strict signal decode by default

- Origin: C3; amendments from A (stable code), B (browser feedback),
  E (same-PR doc fix) — all applied, strictest union.
- Change: encoding.go — flip the default; shape/overflow mismatches
  reject the action with a sanitized 422 carrying a stable error code
  from the P2.12 scheme, assigned once now. `WithLenientDecode()` is
  the explicit opt-out; passing both decode options panics. Remove the
  EXPERIMENTAL label from strictness. stability.md:48 and every
  lossy-decode doc example corrected in this PR.
- Verify: `TestSignal_rejectsOverflowingClientValueByDefault`,
  `TestSignal_returns422OnShapeMismatch`,
  `TestSignal_truncatesOnlyWithLenientOptIn`,
  `TestConfig_panicsOnConflictingDecodeOptions`; browser milestone:
  a 422-rejected action produces observable client feedback, not a
  silently dead button.
- Lifts: Reactive 6.6→8; Ease 5.4→6; kills blocker 7.
- Deps: P0.1, P0.2.
- Size: M.

### P0.7 — drainQueue write-then-clear plus signal-inclusive resync

- Origin: A6, B2, B3 merged (B's design survives).
- Change: sse.go — snapshot the queue under lock without clearing;
  write; on full success compare-and-clear against the snapshot so
  mid-write patches survive; on error the queue stays intact for the
  reconnect drain. Resync emits, in order, the coalesced pending
  signal patch (last-value-wins, from the queue plus a per-ctx
  `lastSignals` map tracking only server-pushed keys, so client-local
  signals are never clobbered), then the view fragment.
- Verify: `TestSSE_redeliversQueuedFrameAfterFailedWrite`,
  `TestSSE_retainsQueuedFramesWhenWriteFails`,
  `TestSSE_reshipsServerPushedSignalsOnReconnect`,
  `TestSSE_doesNotClobberClientOnlySignalsOnResync` (table-driven).
- Lifts: SSE 6.2→7.5; kills the frame-loss/signal-drift blockers.
- Deps: none.
- Size: M.

### P0.8 — Unify all four backplane tailers on one reconnect loop

- Origin: A3, D3 merged (D3's tailLoop survives).
- Change: extract the projector's reconnect loop into a shared
  `tailLoop` (tailer.go): jittered backoff on channel close, bounded
  retry of boot-time Head/Subscribe failures, graceful-stop detection.
  Rewrite the changes and broadcast tailers on it; broadcast re-Heads
  on reconnect (ephemeral semantics); changes/projector resume from
  the last-applied offset (A3's clause). Metrics
  `via.backplane.tailer_reconnect{feed}` and `tailer_up{feed}`.
- Verify: `TestBroadcast_resumesAfterBackplaneBlip` (two Apps, shared
  backplane, vt.AwaitFrame),
  `TestStateApp_convergesAfterChangesTailerReconnect`,
  `TestBroadcast_retriesBootTimeSubscribeFailure`, plus an
  embedded-NATS server-bounce variant; metrics asserted via the
  public hook.
- Lifts: Broadcast 4.6→6; Backplane +reliability; kills blocker 9.
- Deps: none.
- Size: M.

### P0.9 — Durable retention floor plus cluster-wide consumer floor

- Origin: D1, D5 merged.
- Change: extend Compactor with `LowestRetained(ctx, key)` as a
  documented interface-upgrade type assertion (A's amendment, no
  breaking Backplane change), recorded durably floor-first before any
  discard. Delete the `gapsBenign` latch; classify every gap: floor
  <= cur+1 benign, otherwise reseed-or-halt. vianats writes a
  `floor:<key>` cell then purges. registerConsumer CAS-updates a
  durable `consumers:<key>` registry; the compaction floor clamps to
  the cluster-wide minimum durable consumer offset, not the pod-local
  map. The restored durability promise ships in this PR per P0.12.
- Verify: `TestStateAppEvents_haltsOnCompactionOvertake` and
  `TestStateAppEvents_reseedsFromSnapshotAfterOvertake` across
  InMemory, memevents, and vianats (embedded JetStream, two pods);
  `TestCompact_respectsRemoteConsumerFloor`;
  `TestConformance_reportsLowestRetainedAfterCompact`;
  `compaction_gap_halt` fires in the overtake drill.
- Lifts: Backplane 4.2→6.5; kills blocker 2.
- Deps: none (template for P3.7 conformance).
- Size: L.

### P0.10 — Live epoch re-derivation plus meta-feed guardrails

- Origin: D2, D4 merged.
- Change: vianats epoch becomes an atomic refreshed on Head, on
  sequence regression in the subscribe loop, and on every P0.8
  re-subscribe — a live pod surviving stream delete+recreate resets
  the projection instead of silently dropping records. Route
  via.changes/via.broadcast to a separate `<prefix>_meta` stream with
  MaxAge (default 10m, `WithMetaMaxAge`); construction errors if the
  `_ev` stream carries MaxAge/MaxBytes/MaxMsgs.
- Verify: `TestBackplane_reportsNewEpochAfterLiveStreamRecreate` (one
  live Backplane across recreate),
  `TestStateAppEvents_resetsProjectionAfterLiveStreamRecreate`,
  `TestBackplane_failsConstructionOnEventStreamRetention`,
  `TestBackplane_trimsMetaFeedByAge`,
  `TestStateApp_convergesAfterMetaFeedTrim`.
- Lifts: Backplane 6.5→7; kills blocker 8 and the forever-growth gap.
- Deps: P0.8 (reconnect hook), P0.9 (floor discipline).
- Size: M.

### P0.11 — Render-correctness guards: by-value child panic, Fragment

- Origin: A5; Fragment fix added by chair (ruling 16 — blocker 13's
  attribute drop had no owning item in any draft).
- Change: buildDescriptor panics at Mount on a child composition held
  by value, naming the field and the fix; replaces the opt-in
  devChecks lint for this class. h.Fragment stops silently dropping
  attribute arguments: passing an attribute to Fragment panics at
  construction with triage text (registration-time programming
  mistake per conventions).
- Verify: `TestMount_panicsOnByValueChildComposition`,
  `TestFragment_panicsOnAttributeArgument`; dead_bind_test.go cases
  flip from lint-warns to Mount-panics.
- Lifts: Composition 6.2→7.5; h DSL 6.4→7.
- Deps: none.
- Size: S.

### P0.12 — Doc truth pass: drift fixes, durability honesty, markers

- Origin: E1, E2, E4, D6/E3 merged; E16's banner (P0-cheap slice).
- Change: delete the non-compiling `on.Click("DoThing")` claim and
  distinguish vt's string Action API; repair testing.md's first
  snippet (undeclared `p`) as two standalone snippets; fix the
  examples count, add the chatcluster row, make `app.Start()` the one
  bootstrap pattern everywhere (no `ListenAndServe`). Replace
  production.md:128-133,278-281 durability claims with the honest
  limitation now; the promise returns only inside P0.9's PR with its
  named test in the same diff. Install the proof-marker convention
  plus claim-registry file and its CI grep. Add the EXPERIMENTAL
  banner to docs/plugins.md.
- Verify: `TestDocs_noStringActionForms`,
  `TestExamples_tableMatchesDirectories`,
  `TestDocs_noRawListenAndServe`,
  `TestPluginsDoc_carriesExperimentalBanner`; proof-marker gate exits
  0; all snippets compile under P0.2.
- Lifts: Docs 7→7.5; kills blocker 12 and the doc half of blocker 2.
- Deps: P0.2; promise-restore coupled to P0.9.
- Size: M.

## P1 — Harden the edges, open the dispatch window

Phase goal: close every injection, supply-chain, trust, and
resource-exhaustion gap from the verdict, and land the dispatch
mechanism that everything in P2 stands on. C5 signing lands first
because rate limiting, recovery, and the later session store key off
it. Deprecation warnings ship; nothing is deleted yet.

Exit criteria:

- C12 negative-path suite green; every consensus blocker 1-11 has a
  named, permanent regression test.
- Hermetic no-egress CI, scanners, viavet, and the provenance
  manifest are blocking gates.
- Dispatch deprecation warnings emit in a tagged minor release; two
  pods expose identical action-surface hashes.
- A flood test returns 429; disconnected-tab memory is provably
  capped at the configured byte limit.

### P1.1 — HMAC-signed session and tab tokens

- Origin: C5 (absorbing A17's signing paragraph per D).
- Change: token format `v1.<id-hex>.<base64url(HMAC-SHA256)>`;
  `via.WithSessionKeys(keys ...[]byte)` — first signs, all verify.
  adoptSession and tab-token checks accept only MAC-valid tokens.
  No key configured: ephemeral process key plus one warning; enabling
  the backplane without keys panics at startup. Constant-time
  compare. Cookie value format documented opaque, not public API
  (A's amendment).
- Verify: `TestSession_rejectsCookieWithForgedMAC`,
  `TestSession_adoptsSignedCookieAcrossPods`,
  `TestTab_returns404OnForgedTabToken`,
  `TestSession_rotatesVerificationAcrossKeyList`,
  `TestBackplane_panicsWithoutSessionKeys`; P3.7 drill exercises
  cross-pod adoption under key rotation (D's amendment).
- Lifts: Sessions 5.6→7.5; kills blocker 6.
- Deps: none.
- Size: L.

### P1.2 — Ref dispatch mechanism: binding is exposure

- Origin: A7, A8, C16 synthesized per the settled 4-1 vote with E's
  surface concern honored.
- Change: new opaque `via.Ref` plus `via.Do((*T).Method)` resolving
  method expressions by reflect method-set pointer comparison — the
  "-fm" trampoline, boot canary, and methodNameCache are deleted.
  Constructing a Ref registers (type, method); binding IS exposure:
  `on.Click(c.Inc)` keeps working and registers exposure at bind
  time, so tutorials and all 14 examples compile unchanged. Closure
  or top-level-func bindings panic at Mount with the notMethodPanic
  triage text; `via.Do` of a closure panics at construction.
  `via.Expose` is the single programmatic escape hatch (chair ruling
  5). Startup warning lists every exported method that will lose
  exposure at the P2.3 flip. bareAttrCache keyed by Ref. Dispatch
  routes by method identity, intersected with the mounted root.
- Verify: `TestDo_resolvesMethodExpressionAtConstruction`,
  `TestDo_panicsOnClosureAtConstruction`,
  `TestMount_panicsOnClosureBinding`,
  `TestAction_returns404ForRefOfUnmountedComposition` (C's
  cross-composition amendment),
  `TestOnClick_rendersRefWithoutTrampolineParsing`,
  `TestBrowser_refBoundClickFires` (B's amendment); grep shows no
  `"-fm"`; the P3.7 drill asserts identical action-surface hashes
  across pods (D's amendment).
- Lifts: on.* 5.4→7.5; Composition 7.5→8; Actions 6.4→8; kills
  blockers 5 and 11 (flip completes at P2.3).
- Deps: P0.1 (browser milestone).
- Size: L.

### P1.3 — Sink hygiene: jsString, typed keys, UnsafeRaw, viavet

- Origin: A9, B7, C9 merged.
- Change: one unexported `jsString` JSON-encoding helper at every
  expression sink in on/ and computed.go push paths (single,
  grep-auditable; later feeds P3.2's hoister). Typed key constants
  `on.Enter`, `on.Escape`, arrows, plus `on.KeyNamed`; the string
  KeyFilter field is deleted same release. `h.Raw` becomes
  `h.UnsafeRaw` (deprecated alias one minor release; viavet flags the
  alias too, per A's amendment); add `h.JSON` (escaping `</script`,
  `<!--`, `]]>`) and `h.ScriptSrc`. Ship the `viavet` go/analysis
  analyzer flagging non-constant arguments to UnsafeRaw and (later)
  UnsafeScriptPatch; wired into ci-check.sh.
- Verify: `TestOnKey_jsonEncodesHostileKeyName`,
  `TestOn_neutralizesScriptInjectionInEveryEvalSinkInput`
  (table-driven hostile cases),
  `TestBrowser_keyFilterWithQuoteCharDoesNotExecute`,
  `TestJSON_escapesScriptCloseSequence`,
  `TestViavet_flagsDynamicUnsafeRawArgument`.
- Lifts: on.* 7.5→8.5; h DSL 7→8.5; kills blocker 13's on.Key splice.
- Deps: P0.1.
- Size: M.

### P1.4 — Upload hardening

- Origin: A11, C10 merged (complementary halves, union of Verifies).
- Change: enforce declared caps during the copy (LimitReader plus
  overread sentinel), error names cap and field; Mount panics when a
  MultipartReader-consuming action coexists with typed File/Files
  fields; isFileType switches to reflect.Type identity. Add
  `SniffContentType`/`MatchesDeclaredType`, `via.SafeFilename`
  (table-driven traversal stripping), `File.SaveUnder` with post-join
  prefix check; MultipartReader enforces the typed caps; Save
  cross-checks written bytes against declared size.
- Verify: `TestFile_saveRejectsPartExceedingDeclaredCap`,
  `TestMount_panicsOnMultipartReaderWithTypedFileFields`,
  `TestMount_bindsAliasedFileTypeField`,
  `TestFile_rejectsMismatchedDeclaredContentType`,
  `TestSafeFilename_stripsTraversalSequences`,
  `TestFile_saveUnderRefusesEscapeFromDirectory`,
  `TestMultipartReader_enforcesTypedSizeCaps`,
  `TestFile_saveErrorsOnSizeMismatch`.
- Lifts: Uploads 5.4→8.
- Deps: none.
- Size: M.

### P1.5 — Explicit proxy trust plus rate-limiting middleware

- Origin: C8 plus B9/C7 merged (C7 survives on dependency hygiene;
  B9's LRU cap and Retry-After fold in).
- Change: `mw.RedirectHTTPS()` trusts only direct TLS state;
  X-Forwarded-Proto honored only via `mw.WithTrustedProxies(CIDRs)`
  with peer-address checks; invalid CIDR panics; the resolver is
  exported for reuse. `mw.RateLimit(opts...)`: token bucket keyed by
  the MAC-verified sid, falling back to resolver-derived client IP —
  never raw X-Forwarded-For; defaults 60 action POSTs/min, 20
  renders/min; 429 plus Retry-After; LRU-capped buckets; WithRate,
  WithBurst, WithKeyFunc.
- Verify: `TestRedirectHTTPS_ignoresForwardedProtoFromUntrustedPeer`,
  `TestRedirectHTTPS_honorsForwardedProtoFromTrustedCIDR`,
  `TestRedirectHTTPS_panicsOnInvalidCIDR`,
  `TestRateLimit_returns429AfterBurstExhausted`,
  `TestRateLimit_keysBySessionNotIP`,
  `TestRateLimit_setsRetryAfterHeader`.
- Lifts: Config/ops 7.5→8.5; kills blocker 13's rate-limit and
  X-Forwarded-Proto gaps.
- Deps: P1.1.
- Size: M.

### P1.6 — Referer-free SSE recovery

- Origin: C11; B's amendment (one snapshot, two consumers).
- Change: snapshot page route and params server-side in the tab
  record at first render; recoverSSE rebuilds keyed by the
  MAC-verified tab token and ignores Referer entirely; OnInit re-runs
  only against server-stored params. This snapshot is exactly the
  record P3.1's rebootstrap path reads — one snapshot, two consumers.
- Verify: `TestRecover_rebuildsPageFromServerStoredParams`,
  `TestRecover_ignoresForgedRefererHeader`.
- Lifts: SSE 7.5→8.
- Deps: P1.1.
- Size: M.

### P1.7 — SSE replay window plus keepalive and anti-buffering

- Origin: B4 (moved P0→P1, agreed) plus B18 (added; folded by chair).
- Change: monotonic per-ctx event ids; bounded replay ring
  (`via.WithSSEReplayWindow(n)`, panics on negative); Last-Event-ID
  inside the ring replays missed frames verbatim, outside falls back
  to P0.7 resync. Keepalive comments at a stated interval;
  anti-buffering headers (`X-Accel-Buffering: no`, `Cache-Control:
  no-cache, no-transform`); production.md reverse-proxy section.
- Verify: `TestSSE_replaysMissedFramesFromLastEventID`,
  `TestSSE_fallsBackToResyncWhenReplayWindowExceeded`,
  `TestSSE_assignsMonotonicEventIDs`,
  `TestSSE_emitsKeepaliveCommentWithinInterval`,
  `TestSSE_setsAntiBufferingHeaders`,
  `TestBrowser_reconnectResumesWithoutFullRerender`,
  `TestBrowser_streamStaysLiveBehindBufferingProxy` (nginx-default
  container).
- Lifts: SSE 8→9.
- Deps: P0.1, P0.7.
- Size: M.

### P1.8 — Bounded coalescing patch queue plus render coalescing

- Origin: A12, B5, D8 merged (B5's architecture survives) plus D7 as
  the connected-side complement, per the settled cluster.
- Change: disconnected tabs coalesce — only the latest full-view
  render kept, signals merge last-value-wins, explicit patches capped
  by `via.WithTabQueueBytes(n)` (chair ruling 2: one name,
  byte-denominated, default 1 MiB, 0 panics). Overflow sets
  needsResync; reconnect honors it, falling back to the stale-tab
  rebootstrap when the ctx is dead; `via_tab_queue_dropped_total`
  increments. Connected side (D7): per-Ctx renderPending atomic plus
  a single drain worker collapsing N fold/broadcast wakeups into one
  follow-up render, goroutines O(tabs). Shared invariant, asserted in
  both halves' tests: a newer full render supersedes any pending
  older one, and the drain worker is the only writer into a tab's
  queue (what makes compare-and-clear race-free).
- Verify: `TestSSE_coalescesAutoRendersWhileDisconnected`,
  `TestSSE_forcesResyncAfterQueueOverflow`,
  `TestBroadcast_coalescesRendersUnderBurst` (1000-fold burst, far
  fewer renders, correct final frame),
  `TestApp_boundsGoroutinesUnderFanOutStorm`; D7's burst test runs
  connected and disconnected (D's amendment).
- Lifts: SSE 9→9.5; Broadcast 6→7; kills blocker 13's unbounded
  queue and fan-out blowup.
- Deps: P0.7.
- Size: M.

### P1.9 — Runtime guards and ops health

- Origin: A4 (moved out of P0, agreed) plus D9, D11; folded by chair.
- Change: SyncNow inside an action returns an error naming the
  auto-flush contract instead of deadlocking (runtime condition, not
  panic). Periodic backplane liveness probe drives
  `via.backplane.up`; `WithReadyzBackplane()` folds backplane health
  into readiness, off by default; 503 body stays generic, details go
  to logs (C's amendment). `WithFoldDigest(mode)` with Sampled as
  default removes the O(state) per-event encode.
- Verify: `TestCtx_syncNowReturnsErrorInsideAction`,
  `TestReadyz_reports503WhenBackplaneDown`,
  `TestReadyz_staysReadyWithoutBackplaneOption`,
  `TestFoldDigest_emitsAtMostOncePerInterval`,
  `TestFoldDigest_detectsCrossPodDivergenceWhenSampled`; P3.7 drill
  flips `via.backplane.up` within 10s and back on heal.
- Lifts: Config/ops 8.5→9; Actions 8→8.5; kills blocker 13's
  SyncNow and /readyz gaps.
- Deps: P0.8.
- Size: M.

### P1.10 — Loopback cookie auto-detection

- Origin: A21 (added); C's and B's amendments, both applied.
- Change: detect loopback by the listener's bound address only
  (127.0.0.0/8, ::1) — never Host or X-Forwarded-* headers — and drop
  the Secure attribute with one logged notice. `WithInsecureCookies`
  remains the override; conflicting force-secure options panic.
  getting-started, tutorial, and troubleshooting give one answer.
- Verify: `TestCookies_insecureOnLoopbackByDefault`,
  `TestCookies_secureOnNonLoopback`,
  `TestCookies_keepsSecureWhenHostHeaderSpoofsLocalhost`, plus a
  browser-harness regression (cookie rejection is browser-enforced).
- Lifts: Ease 6→7; DX 5.8→6.5.
- Deps: P0.1.
- Size: S.

### P1.11 — Negative-path security suite

- Origin: C12; C's round-2 extension amendment.
- Change: external-package negative tests for every blocker, through
  real httptest servers: forged/absent/cross-session tab tokens on
  action POST and SSE handshake; broadcast scoping isolation; upload
  traversal and cap bypass; no integrity-less third-party script in
  any rendered head; strict-decode rejection. Extended as round-2
  items land: the P2.3 unbound-404 flip, unified-Broadcast scoping,
  P3.4 upload tokens, P3.1 rebootstrap forgery. This suite is the
  single home of every blocker's permanent regression test.
- Verify: `TestAction_returns404OnMissingTabToken`,
  `TestAction_rejectsTabTokenFromOtherSession`,
  `TestSSE_rejectsForgedHandshakeToken`,
  `TestBroadcast_neverCrossesSessionBoundary`,
  `TestUpload_refusesPathTraversalFilename`,
  `TestPage_rendersNoThirdPartyScriptWithoutIntegrity`.
- Lifts: vt/testing 8→8.5; Sessions +0.5; Broadcast +0.5.
- Deps: P1.1-P1.6 (tests written first, red until each lands).
- Size: M.

### P1.12 — Security docs: SECURITY.md, threat model, checklist

- Origin: C13 plus C14/E7 merged (absorbing A19's CSRF doc); E's
  checkable-grep amendment.
- Change: SECURITY.md (disclosure policy, supported versions);
  docs/threat-model.md (trust boundaries, token semantics, blast
  radii, plugin supply chain, what via does not defend) — the single
  home of the real CSRF model; delete runtime.go's "see memory"
  comment in favor of a pointer to the doc. production.md gains the
  generated-from-real-code security checklist (embed/SRI, CSP
  rationale, strict decode, trusted proxies, rate limits, upload
  helpers). jsdelivr mentions allowed only inside fenced blocks that
  contain `integrity=` or lines in docs/_cdn-allowlist.txt (E's
  amendment replacing the unjudgeable "adjacent" grep).
- Verify: ci gates `test -f SECURITY.md` and the forbidden-pattern
  check; `ExampleCSP`, `ExampleWithCDN`, `ExampleRateLimit` compile
  under P0.2; `grep -rn "see memory" .` empty.
- Lifts: Docs 7.5→8.5; 1.0 +1.
- Deps: P0.5, P0.4, P1.5.
- Size: M.

### P1.13 — CI gates: hermetic, scanners, viavet, provenance

- Origin: C15 plus A20's hermetic job (moved up per A and B) plus C18
  (added; folded by chair).
- Change: ci-check.sh becomes the single security gate: `go test
  -race ./...` under `unshare -n` (no egress — locks in P0.4
  forever), gitleaks, govulncheck, staticcheck, go vet plus viavet,
  SRI/doc greps. C18: a checked-in provenance manifest (upstream URL,
  version, sha256 per file) for datastar.js and every embedded plugin
  asset; refresh scripts fail on upstream-hash mismatch; CI recomputes
  embedded hashes against the manifest.
- Verify: `./ci-check.sh` exits 0 on a clean tree; seeded-failure
  fixtures prove each tool's nonzero exit propagates;
  `TestAssets_matchProvenanceManifest`.
- Lifts: 1.0 +1; Plugin 6.5→7 (supply-chain story complete).
- Deps: P0.4, P1.3.
- Size: M.

## P2 — One breaking window, one migration

Phase goal: every planned pre-1.0 API break lands in a single
deprecation window so users migrate once: middleware shape, session
keys, unified Broadcast, error-returning verbs, the dispatch
enforcement flip, per-row Ref args, the session store, and ordered
shutdown — with the docs build-out documenting the new surfaces as
they land, errors cataloged last after panic texts freeze.

Exit criteria:

- One migration guide; the apidiff report against the pre-P2 tag
  matches MIGRATION.md anchors exactly (P0.2 job green).
- All 14 examples plus viashowcase migrated in-tree; no deprecated
  symbol remains.
- An exported-but-unbound method POST returns 404 (negative suite
  flipped to permanent).
- Sessions survive an App restart in CI.
- E9 docs-coverage gate reports zero undocumented exports.

### P2.1 — Standard http.Handler middleware shape

- Origin: A13; E's same-PR docs amendment.
- Change: `type Middleware = func(http.Handler) http.Handler`;
  `via.Wrap` adapts the legacy three-arg shape; mw builtins convert
  in the same commit; old shape removed next release.
  routing-sessions-middleware.md and the MIGRATION entry with the
  one-line Wrap recipe ride the PR.
- Verify: `TestApp_acceptsStandardHTTPHandlerMiddleware`,
  `TestWrap_adaptsLegacyThreeArgMiddleware`; mw tests green
  unchanged.
- Lifts: Sessions/mw 7.5→8; DX 6.5→7.
- Deps: none.
- Size: M.

### P2.2 — Named typed session keys

- Origin: A14; E14's rewrite folds in per the same-PR rule.
- Change: `sess.NewKey[T](name)` returning `Key[T]` with
  Get/Put/Delete; duplicate or empty name panics; the sessbridge
  namespace unifies under the key registry; legacy type-keyed
  Get/Put become deprecated wrappers, removed at 1.0;
  adoption-at-capacity logs WARN and increments
  `via_session_adoption_rejected_total`. The docs page documents
  Key[T] as primary; the type-clobber explanation moves to
  MIGRATION.md (E's amendment; the clobber-workaround tutorial is
  cut, chair ruling 7).
- Verify: `TestSessKey_storesTwoValuesOfSameTypeUnderDistinctNames`,
  `TestSessNewKey_panicsOnDuplicateName`,
  `TestSessKey_isolatesPluginNamespaceFromTypedKeys`,
  `TestApp_logsAndCountsSessionAdoptionAtCapacity`.
- Lifts: Sessions/mw 8→9; Ease 7→7.5.
- Deps: none (P2.6 serializes this model).
- Size: M.

### P2.3 — Dispatch enforcement flip plus typed per-row Ref args

- Origin: A7/A8 flip half, C12 flip, A22 (added) — per settled vote.
- Change: the breaking flip — an exported method with no constructed
  Ref and no binding 404s; the P1.2 startup warning becomes the
  404; deprecated forms (h.Raw alias, legacy middleware/session
  shims scheduled here) delete. A22: `via.Do1[C,T]` /
  `ref.With(arg)` typed per-row arguments — `on.Click(delRef.
  With(item.ID))` dispatches the typed id with no forwarder signal;
  arg type mismatch panics at construction. todos and maps examples
  refactor off the contortion; the README apology paragraph is
  deleted; E11's guide documents the new pattern (chair ruling 6).
- Verify: `TestApp_returns404ForUnreferencedExportedActionMethod`
  (flips permanent in P1.11's suite),
  `TestApp_routesActionExposedViaExpose`,
  `TestDo1_bindsTypedArgPerRow`,
  `TestDo1_panicsOnArgTypeMismatchAtConstruction`; all examples
  green; MIGRATION anchors for the flip.
- Lifts: on.* 8.5→9.5; Composition 8→9; Ease 7.5→8.5; closes
  blockers 5 and 11 fully.
- Deps: P1.2.
- Size: L.

### P2.4 — Unified scoped Broadcast

- Origin: A15 per settled vote, absorbing C6 (ToTopic, bounded pool,
  Unsafe naming), D10 (timeout, counter), B10's surviving receipt
  semantics; B10 cut.
- Change: one entry point — `Broadcast(ctx context.Context, p Patch,
  opts ...BroadcastOption) (BroadcastReceipt, error)`. Options
  ToRoute, ToSession, ToTabs(pred), ToTopic (implemented over the
  predicate path; `Ctx.Subscribe(topic)` is the only new subscription
  surface — chair ruling 4b). Patch constructors NotifyPatch and
  SignalsPatch keep one-liners; script delivery exists only as
  `via.UnsafeScriptPatch(js)` — viavet flags non-constant arguments;
  the raw `Broadcast(script string)` is deleted, never renamed.
  Receipt is honest pod-local: `{LocalTabs int, Appended bool}`. ctx
  flows into Append with a 5s default deadline and the
  `via.broadcast.append_timeout` counter (D10 absorbed; chair ruling
  3: no P1 interim option). Fan-out: C6's bounded worker pool feeds
  P1.8's per-Ctx drain workers (chair ruling 4a). production.md's six
  stale Broadcast lines are patched in this PR (E's amendment).
- Verify: `TestApp_broadcastDeliversPatchToAllTabs`,
  `TestApp_broadcastScopesToRoute`,
  `TestApp_broadcastIsolatesTopicsAcrossSubscribers`,
  `TestApp_broadcastHonorsContextCancellation`,
  `TestApp_broadcastReceiptReportsLocalCountAndAppend`,
  `TestBroadcast_returnsErrorWhenBackplaneStalls`; two-pod inmemory
  vt test; P3.7 drill asserts cross-pod delivery on heal and
  `Appended=false` surfaced while partitioned (D's amendment).
- Lifts: Broadcast 7→9; DX 7→7.5; kills blocker 13's unscoped
  raw-JS Broadcast.
- Deps: P0.8 (hard, per settled vote), P1.8, P1.3 (viavet).
- Size: L.

### P2.5 — Error-returning Op verbs

- Origin: A16; E's doc-sweep amendment.
- Change: every shape verb returns the update error instead of
  discarding it; Div's divide-by-zero becomes a returned error;
  Signal/StateTab verbs return the same type for uniformity with a
  why-godoc. Every Op-verb doc snippet changes shape in this PR, with
  one manual sweep for snippets that compile while discarding the
  error (the gate cannot see that class).
- Verify: `TestNumOps_addReturnsRejectedUpdateError`,
  `TestNumOps_divReturnsErrorOnZeroDivisor`, compile-time
  table sweep across the five families.
- Lifts: Shapes 7→9.
- Deps: P0.3.
- Size: M.

### P2.6 — Durable pluggable SessionStore

- Origin: A17, D12 merged; signing carved out to P1.1 (C's finding).
- Change: `SessionStore` interface (Load/Save/Delete/Touch),
  in-memory default unchanged, backplane-KV impl in-tree;
  `WithSessionStore`; sessstoretest conformance suite for
  third-party stores. Hard precondition: P1.1 — the store persists
  only MAC-signed tokens (persisting unsigned bearers durably is
  strictly worse than today). Wire format serializes the P2.2
  named-key model. Chair ruling 1 places this at P2 (late in phase,
  after P2.2) so P3.1's authenticated milestone has a settled dep.
- Verify: `TestApp_restoresSessionAcrossAppRestartWithPersistentStore`
  (two sequential Apps, one store, cookie survives),
  `TestSession_evictsExpiredFromStore`,
  `TestApp_rejectsUnsignedSessionIDWhenClustered` (mandatory, per
  C's amendment); conformance green on both in-tree stores.
- Lifts: Sessions/mw 9→9.5; 1.0 +0.5; kills blocker 13's
  deploy-equals-logout.
- Deps: P1.1 (hard), P2.2.
- Size: L.

### P2.7 — Backplane ergonomics: migration names, registration I/O

- Origin: D13, D14 merged by chair.
- Change: snapshot migrations keyed by explicit stable names, not
  reflect type strings; Mount panics at startup on a persisted
  snapshot with no registered decode path (panic text gets a stable
  code in P2.12's catalog, per A's amendment); boot probe decodes the
  latest snapshot per key. registerConsumer performs backplane I/O
  before taking consumersMu so a slow backend stops serializing
  OnInit across the app.
- Verify: `TestStateAppEvents_panicsAtMountOnMissingSnapshotMigration`,
  `TestStateAppEvents_migratesSnapshotAcrossTypeRename` (InMemory and
  vianats),
  `TestOnEvent_registersConsumersConcurrentlyDuringBackendStall`.
- Lifts: Backplane 7→8; Ease +0.5.
- Deps: P0.9 (registration path lands after the floor work).
- Size: M.

### P2.8 — Ordered graceful shutdown

- Origin: D19 (added).
- Change: defined Stop() ordering — drain in-flight actions, flush
  patch queues, emit the `via-handover` SSE event with retry hint,
  Unregister durable consumers (P0.9 registry), then close the
  backplane connection and SSE writers. The server half every deploy
  story (P3.1, P2.6, docs) silently assumed.
- Verify: `TestApp_drainsInFlightActionsBeforeBackplaneClose`,
  `TestApp_emitsHandoverBeforeClosingSSE`; P3.7 rolling-restart drill
  asserts zero lost broadcasts and zero orphaned consumer-registry
  entries.
- Lifts: Config/ops 9→9.5; SSE +0.5.
- Deps: P0.8, P0.9.
- Size: M.

### P2.9 — Public asset pipeline

- Origin: B12; C's headers amendment, A's single-pipeline amendment.
- Change: `app.Assets().ServeJS(name, fs)` promotes the P0.4 internal
  serving path to public API — one pipeline, not two. Content-hashed
  immutable paths, sourcemaps, nonce-compatible, plus
  `X-Content-Type-Options: nosniff`. Migrate every inline Go-string
  blob (reconnect manager, toast, echarts/maplibre/picocss runtimes)
  to real embedded .js files. The ci grep ban on inline plugin
  scripts holds until viavet subsumes it, then deletes (one
  enforcement point).
- Verify: `TestAssets_servesNoncedJSWithContentHashCaching`,
  `TestEcharts_runtimeServedAsStaticAssetNotInline`; ci grep exits 0.
- Lifts: Plugin 7→8.5; DX +0.5.
- Deps: P0.4.
- Size: M.

### P2.10 — vt drop/reconnect simulation

- Origin: B13.
- Change: vt gains `DropSSE()` (server-visible sever), `Reconnect()`
  (re-handshake with recorded Last-Event-ID), `ReconnectStale()` —
  the P0.7/P1.7/P1.8 guarantees stay testable in-process forever, and
  app authors can assert their own apps survive blips.
- Verify: `TestVT_replaysFramesAcrossSimulatedDrop`,
  `TestVT_rebootstrapsStaleTabOnReconnect`.
- Lifts: vt 8.5→9.5.
- Deps: P1.7.
- Size: M.

### P2.11 — Docs content build-out

- Origin: E8, E11, E12, E14, E21 merged (A19's coverage slice and
  B17's reference half absorbed; E21 was an added item, folded here
  by chair ruling 10).
- Change: client-reactivity guide (Local/Computed/Effect, all on.*
  modifiers) with the "which state type do I want" decision table as
  its spine; at least one guide example verified in the browser
  harness (B's amendment). Per-row actions guide rewritten against
  `ref.With`. Learning path resequenced: chat reverts to
  StateAppSlice, chatcluster is the explicitly-preview distributed
  example, preview symbols stay below an explicit `<!-- fold -->`
  marker in index.md (E's greppable amendment; D confirms
  preview-tier until P3.8 passes). Sessions page documents Key[T]
  plus the store recipe. docs/file-uploads.md rewritten for the
  P1.4 surface (E21), with the P3.4 chunked addendum to follow.
- Verify: all snippets compile under P0.2;
  `TestExamples_chatUsesSlice`; fold-marker grep; one browser-run
  guide example; coverage list feeds P2.12.
- Lifts: Docs 8.5→9; Ease 8.5→9.
- Deps: P0.2, P2.2, P2.3, P2.5.
- Size: L.

### P2.12 — Reference gates and error catalog

- Origin: E9, E15, E10, E13 merged by chair; A's sequencing
  amendment (catalog freezes last in phase).
- Change: docs-coverage gate — every exported symbol of via, on, vt,
  h, sess, mw, and plugins appears in docs or in an exclusions file
  auto-fed by the deprecation ledger (A's amendment). Generated
  options reference from godoc of every `With*` option, staleness
  CI-checked, consuming the post-P2 surface. Error catalog: every
  user-facing panic and 4xx/5xx string gets a stable short code
  (`via: [E0xx] ...`) and an anchored cause/fix entry — including
  the roadmap-born panics (by-value child, multipart mode-mix,
  closure binding, option conflicts, strict-decode 422) — and wire
  bodies stay sanitized: full text in docs, code plus generic
  message over the wire (C's amendment). Troubleshooting gains the
  dead-bind, child-404, EvalError, and unbound-404 entries.
- Verify: `TestDocsCoverage_failsOnUnlistedExport`,
  `TestOptionsReference_isFresh`,
  `TestErrorCatalog_coversAllPanicStrings`; link checker green.
- Lifts: Docs 9→9.5; DX 7.5→8.5.
- Deps: P2.11, all P2 panic-text churn (catalog lands last).
- Size: M.

## P3 — Distributed and flagship proof

Phase goal: make the distributed and flagship promises on the
now-final API, and prove them falsifiably: nonce-only CSP that
actually executes in a browser, deploys without a reload, a normative
broadcast delivery contract, durable crypto-shred, and the
conformance-plus-drill machinery that continuously re-proves P0-P2.

Exit criteria:

- The three nonce-only browser milestones green under `mw.CSPStrict`
  with zero console errors.
- Cluster drill green: partition, overtake, recreate, meta-trim, and
  rolling restart, each asserting its named metric; zero
  `location.reload()` events during the rolling-restart drill.
- Conformance suite green on InMemory, memevents, and vianats —
  embedded JetStream, default CI, no integration tags.
- Every affirmative safety claim in docs carries a proof marker
  naming a green test.

### P3.1 — Seamless deploy handover

- Origin: B14; C's auth amendment, D's dependency amendments.
- Change: on graceful shutdown (P2.8) the server emits
  `via-handover`; the reconnect manager (a real .js asset per P2.9)
  attempts in-place rebootstrap via `X-Via-Rebootstrap` instead of
  `location.reload()`, reading the P1.6 server-side snapshot.
  The rebootstrap path requires a MAC-valid session cookie and is
  covered by the SSE-handshake rate-limit bucket (C's amendment —
  not an unauthenticated context factory). Anonymous and
  authenticated cases ship as separately named tests; the
  authenticated one hard-deps P2.6 (D's amendment).
- Verify: `TestSSE_bootsFreshContextOnRebootstrapHeader`,
  `TestSSE_rejectsRebootstrapWithForgedSessionCookie`,
  `TestBrowser_gracefulRestartPreservesScrollAndSignals`; P3.7
  rolling-restart drill asserts zero reload events.
- Lifts: SSE 9.5→10.
- Deps: P2.8, P2.6, P1.1, P1.5, P1.6, P2.9.
- Size: M.

### P3.2 — CSP-strict expression hoisting and honest mw.CSPStrict

- Origin: B15 per settled vote; C's keying amendment (stricter).
- Change: under `via.WithCSPStrictExpressions()`, every
  server-emitted expression is hoisted into one nonce-carrying
  registry script keyed by SHA-256 or the exact expression string —
  never fnv1a (collidable, and expressions embed user-derived data).
  An evaluator shim prefers precompiled functions; the `Function()`
  fallback fails loud with a named console error, never silently
  widens. SSE-pushed expressions ride the nonce machinery as hoist
  patches. `mw.CSPStrict()` ships in the same release, emits the
  nonce-only policy, and panics at registration without the option.
  The hoister consumes P1.3's single jsString path; a grep gate
  asserts no second escaping path exists.
- Verify: `TestCSPStrict_hoistsEveryRenderedExpression`,
  `TestCSPStrict_panicsWithoutExpressionHoisting`,
  `TestBrowser_clickFiresUnderNonceOnlyCSP`,
  `TestBrowser_computedAndKeyFilterEvalUnderNonceOnlyCSP`,
  `TestBrowser_ssePushedScriptRunsUnderNonceOnlyCSP` (all zero
  console errors); CI grep asserts 'unsafe-eval' absent from the
  strict path.
- Lifts: Config/ops 9.5→10; on.* 9.5→10.
- Deps: P0.1, P0.5, P1.3.
- Size: L.

### P3.3 — Datastar runtime upgrade contract

- Origin: B19 (added).
- Change: a shim-contract suite pins the evaluator hooks P3.2 wraps;
  a scripted vendored-datastar bump dry-run must leave the entire
  TestBrowser_* suite (including the three nonce-only milestones)
  green before any datastar.js version change merges; wired into
  ci-check.sh; the P1.13 provenance manifest records the bump.
- Verify: the gate job; a fixture bump with a broken hook fails CI.
- Lifts: 1.0 +0.5; protects P3.2 permanently.
- Deps: P3.2, P0.1, P1.13.
- Size: M.

### P3.4 — Chunked resumable uploads (EXPERIMENTAL)

- Origin: B11 (moved P2→P3, agreed); C's token amendments and D's
  bounding amendments, both applied.
- Change: `via.Upload` field type; chunked POSTs to
  `/_upload/{token}/{index}` with resume-by-query; progress as a
  built-in `Signal[int]`. Tokens are cryptographically random and
  bound to the issuing session; endpoints sit behind the rate-limit
  bucket (C). Tokens get a 1h TTL with an orphan sweep;
  `WithUploadDir` gets a max-bytes cap (507/429 on exceed);
  pod-affinity requirement and the P2.8 drain interaction documented
  under the proof-marker rule (D). Inherits P1.4 hard: caps enforced
  at chunk and assemble time, SafeFilename on metadata. Ships
  EXPERIMENTAL.
- Verify: `TestUpload_resumesFromNextChunkAfterDrop`,
  `TestUpload_rejectsAssemblyOnSizeMismatch`,
  `TestUpload_rejectsTokenFromOtherSession`,
  `TestUpload_rejectsForgedToken`,
  `TestUpload_sweepsExpiredPartialAssemblies`,
  `TestUpload_sanitizesClientFilename`,
  `TestBrowser_uploadProgressSignalReachesHundredPercent`.
- Lifts: Uploads 8→9.
- Deps: P0.1, P2.9, P1.4, P1.1, P1.5, P2.8.
- Size: L.

### P3.5 — Broadcast delivery contract page

- Origin: A18, E19 (moved P4→P3), B17's delivery half merged.
- Change: one normative page: at-least-once delivery while tailers
  are live, bounded redelivery after a blip (P0.8 offsets), receipt
  meaning per topology, Patch.Signals total order defined as
  backplane append order, retention defaults plus
  `WithBroadcastRetention`, when-Broadcast-vs-StateApp guidance, and
  a compiling notification example. Every guarantee carries a proof
  marker naming its conformance test. Verified on embedded JetStream
  in default CI — no Docker integration tags (D's amendment,
  stricter).
- Verify: `TestBroadcast_deliversAcrossTwoAppsOnSharedBackplane`,
  `TestBroadcast_ordersSignalPatchesByAppendOrder` — the ordering
  test also runs inside the P3.7 partition drill (D's amendment);
  proof-marker gate green over the page.
- Lifts: Broadcast 9→10; Docs +0.5.
- Deps: P2.4, P0.8, P3.7.
- Size: M.

### P3.6 — Persistent KeyStore and NATS auth guidance

- Origin: C17; D's crash-ordering amendment.
- Change: `vianats.KeyStore` backed by NATS KV — per-record DEKs
  wrapped by a KEK; Shred deletes the wrapped DEK cluster-wide;
  in-memory store documented test-only. Shred respects the P0.9
  floor discipline (shred-then-compact ordering) so a crash between
  DEK delete and purge cannot leave a readable record the floor
  claims gone. nats.Option passthrough for creds/TLS; mandatory-TLS
  and account-isolation guidance in the threat model.
- Verify: `TestNATSKeyStore_persistsKeysAcrossReconnect`,
  `TestNATSKeyStore_shredMakesRecordUnreadableClusterWide`,
  `TestNATSKeyStore_shredSurvivesCrashBeforePurge` (conformance).
- Lifts: Backplane 8→8.5.
- Deps: P1.1, P0.9, P1.12.
- Size: L.

### P3.7 — Conformance frontier and multi-pod cluster drill

- Origin: D15, D16 merged by chair.
- Change: backplanetest gains mandatory cases for the P0 safety
  frontier: LowestRetained after Compact, live epoch change,
  resubscribe-loses-no-record, meta-trim tolerance — every
  third-party backend inherits the tests the reference backend
  failed. ci-check.sh gains the cluster-drill job: three real App
  pods on embedded NATS (single and 3-node matrix) under load with
  rolling restart, partition, compaction overtake, live recreate,
  and meta-trim. Each drill asserts its named metric:
  `compaction_gap_halt` (overtake), `tailer_reconnect` (partition),
  epoch-reset reseed (recreate), zero reloads (P3.1), identical
  action-surface hash across pods (P2.3), cross-pod session adoption
  under key rotation (P1.1), broadcast receipt honesty under
  partition (P2.4). Required on backplane/vianats PRs.
- Verify: the job is the artifact; conformance green on all three
  backends in default CI; `-race` clean; zero goroutine leak.
- Lifts: vt/testing 9.5→10; Backplane 8.5→9; 1.0 +1.
- Deps: P0.9, P0.10, P0.8, P1.9.
- Size: L.

### P3.8 — Distributed-GA criteria checklist

- Origin: D17; E's proof-marker amendment (stricter than review
  gates).
- Change: docs/distributed-ga.md — the testable bar for dropping the
  backplane's EXPERIMENTAL label: conformance green on all backends,
  drill green 30 consecutive days, every safety sentence
  proof-marked, durable sessions plus signed tokens plus ordered
  shutdown shipped, alert pack fired by drills. Every checklist line
  carries a proof marker naming its test, metric, or CI job.
  stability.md links it so "preview" has an explicit exit.
- Verify: the marker gate over the checklist; stability link check.
- Lifts: 1.0 +0.5; Docs +0.5.
- Deps: P3.5, P3.7, P0.12.
- Size: S.

## P4 — Permanence and release

Phase goal: convert everything above into permanence. The release
gates make regressions structurally impossible; the final docs pass
makes every claim provable and current; the plugin kit turns
internals into ecosystem; v1.0.0 tags only surfaces that pass every
gate.

Exit criteria:

- ci-check.sh is the single gate; a seeded failure in every job
  demonstrably bites.
- A deliberately broken public signature fails the apidiff job; the
  EXPERIMENTAL sweep fails on a one-sided marker change.
- The backplane ships in v1 only if P3.8's gate held through the
  soak; otherwise it splits into a separately versioned module.
- v1.0.0-rc.1 soaks across all examples plus viashowcase; v1.0.0
  tags with every gate green.

### P4.1 — Release gates: apidiff, stability sweep, v1 audit, soak

- Origin: A20 (hermetic job moved to P1.13 per A and B), C15's
  seeded-failure proof retained there.
- Change: blocking apidiff against the latest tag (incompatible
  change without a version-bump file fails); consistency sweep —
  every stability.md-experimental symbol carries an `EXPERIMENTAL:`
  godoc prefix and vice versa (shares P2.12's symbol walker); the v1
  surface audit: backplane/StateAppEvents either split into a
  separately versioned module or sit behind
  `WithExperimentalBackplane` per the P3.8 outcome (both branches
  pre-authorized, chair ruling 15). Tag v1.0.0-rc.1, soak, v1.0.0.
- Verify: scratch-branch signature break fails CI; sweep fixture
  fails on a removed marker; `go list -m` shows the split or
  `TestApp_panicsOnBackplaneWithoutExperimentalOptIn` passes.
- Lifts: 1.0 →10; Shapes/Plugin/DX permanence.
- Deps: everything prior.
- Size: M.

### P4.2 — Final docs truth pass

- Origin: A19 residue, B17 remnant (snippet machinery ceded to P0.2,
  surface docs to P2.11, CSRF to P1.12 per the merges).
- Change: rewrite the README "compiler understands your UI" claim to
  the synthesized dispatch reality — bind-time method-identity
  resolution, closure failures at Mount/init, no name parsing. Final
  "Delivery guarantees" wording covering P0.7/P1.7/P1.8/P3.1 with
  proof markers. Confirm full on.*/state coverage via the P2.12
  gate; final link-and-anchor pass.
- Verify: snippet gate, coverage gate, proof-marker gate, link
  checker — all green over the final tree.
- Lifts: Docs 9.5→10; DX 8.5→9.5.
- Deps: P2.3, P3.5, P3.1.
- Size: M.

### P4.3 — Plugin author kit

- Origin: B16, E16 merged (banner already landed in P0.12); A10's
  plugintest helpers fold in.
- Change: docs/plugin-authoring.md (interface contract, asset
  pipeline, CSP/SRI requirements, `Plugin(...)` constructor naming
  per CONVENTIONS); runnable `examples/plugin-template` external
  module whose source is the guide's snippets;
  `vtbrowser.PluginConformance` — registers without network, serves
  hash-cached nonced assets, zero console errors, no inline
  UnsafeRaw. Official plugins must pass it.
- Verify: `TestPluginConformance_passesForAllOfficialPlugins`
  (table-driven over picocss/echarts/maplibre);
  `go test ./examples/plugin-template/...` exits 0.
- Lifts: Plugin 8.5→10.
- Deps: P0.1, P2.9.
- Size: M.

### P4.4 — Ops alert pack and runbooks

- Origin: D18.
- Change: Prometheus rule files for the catalogue
  (compaction_gap_halt > 0, tailer_up == 0, backplane.up == 0,
  digest mismatch, append_timeout rate) plus one runbook page per
  alert (meaning, blast radius, first three commands); promtool lint
  in ci-check.sh; the P3.7 drills fire the partition and overtake
  alerts against a scrape of the test pods.
- Verify: promtool exit 0; drill-fired alerts asserted.
- Lifts: Config/ops →10 (held); Ease +0.5 for operators.
- Deps: P1.9, P3.7.
- Size: M.

### P4.5 — Docs site: versioned publishing and lint gates

- Origin: E17, E18 merged by chair.
- Change: snapshot-on-tag versioned docs (/vX.Y/, latest default,
  /dev/ with unreleased banner) landing before the 1.0 tag;
  link/anchor checker plus markdownlint configured to CONVENTIONS.md
  (80-char lines, blank-line-wrapped lists, headings over emphasis),
  existing violations fixed in the same PR.
- Verify: publish job from a test tag produces /vX.Y/ correctly;
  fixtures prove the linter fails on an 81-char line and a dead
  anchor; both jobs exit 0 on tree.
- Lifts: Docs →10 (held); 1.0 +0.5.
- Deps: P2.11, P4.2 (after the big rewrites).
- Size: M.

## Score-lift ledger

All 18 scored features, current final score to post-roadmap target.

| Feature              | Now | Target | Driven by                       |
|----------------------|-----|--------|---------------------------------|
| Composition model    | 6.2 | 10     | P0.11, P1.2, P2.3               |
| Reactive state       | 6.6 | 10     | P0.6, P0.7, P2.11               |
| Shapes + Op verbs    | 5.8 | 10     | P0.3, P2.5, P4.1                |
| h HTML DSL           | 6.4 | 10     | P0.11, P1.3, P2.9               |
| on.* event binding   | 5.4 | 10     | P1.2, P1.3, P2.3, P3.2          |
| Actions & lifecycle  | 6.4 | 10     | P1.2, P1.9, P2.3                |
| SSE transport        | 6.2 | 10     | P0.7, P1.7, P1.8, P3.1          |
| Broadcast            | 4.6 | 10     | P0.8, P1.8, P2.4, P3.5          |
| Sessions/routing/mw  | 5.6 | 10     | P1.1, P2.1, P2.2, P2.6          |
| File uploads         | 5.4 | 9      | P1.4, P3.4                      |
| Plugin system        | 3.8 | 10     | P0.4, P1.13, P2.9, P4.3         |
| Backplane/dist state | 4.2 | 9      | P0.9, P0.10, P3.6, P3.7, P3.8   |
| Testing harness (vt) | 6.2 | 10     | P0.1, P1.11, P2.10, P3.7        |
| Config/metrics/ops   | 6.4 | 10     | P0.5, P1.5, P1.9, P3.2, P4.4    |
| Docs & examples      | 5.6 | 10     | P0.2, P0.12, P2.11, P2.12, P4.5 |
| Overall DX           | 5.8 | 10     | P1.2, P1.10, P2.x window, P4.2  |
| Ease of use          | 5.4 | 9      | P1.10, P2.3, P2.11              |
| 1.0 readiness        | 3.6 | 10     | every exit gate; P4.1           |

Justifications for the three sub-10 caps:

- File uploads cap at 9: the panel recorded uploads as an
  irreducible lens difference (4-6 terminal spread), and P3.4 ships
  EXPERIMENTAL at v1 by design — a surface carrying that label
  cannot honestly score 10 until its label exits.
- Backplane caps at 9: the GA label is gated on P3.8's 30-day drill
  soak, which may complete after the v1 tag; in that branch the
  backplane ships as a separately versioned module (P4.1). Correct,
  conformance-proven, and honest — but 10 is reserved for the GA
  label itself.
- Ease of use caps at 9: the second irreducible lens difference
  (4-6 terminal). The Ref synthesis keeps `on.Click(c.Inc)` and
  `ref.With` kills the forwarder contortion, but the method-on-struct
  ceremony that drove the low lens score is a deliberate design
  identity the roadmap does not (and should not) erase.

## Chair rulings

1. SessionStore phase: P2, not P3 (panel split 3-2 for P3). D's
   dependency argument is decisive — P3.1's authenticated milestone
   hard-deps the store, and blocker 13's deploy-equals-logout should
   not wait two phases. Ordered after P2.2 (A's wire-format
   argument) with P1.1 as a hard precondition (C's stored-credential
   finding). All three drafts' tests kept.
2. Queue option name: one option, `via.WithTabQueueBytes(n)`
   (byte-denominated per B/D's stronger ops argument; 0 panics;
   default 1 MiB). WithTabQueueLimit, WithSSEQueueLimit, and
   WithMaxPatchQueueBytes do not ship.
3. D10 interim: A's P1 internal-fix variant declined. Per D's own
   cut and the settled vote, the timeout, 5s default, and
   append_timeout counter land only inside P2.4's ctx-threaded
   Broadcast; P0.8 removes the common stall cause first.
4. Broadcast fan-out internals: (a) the settled vote's "C6 bounded
   worker pool" and D's "D7 supersedes it" are reconciled as layers —
   the pool bounds fan-out enqueue, D7's per-Ctx drain worker is the
   scheduling layer and the sole writer into any tab queue; both
   invariants asserted. (b) ToTopic is implemented over the
   predicate path (B's concern) but `Ctx.Subscribe(topic)` ships as
   the subscription surface (C6/D) — a topic option with no
   subscribe primitive is undeliverable.
5. Escape-hatch spelling: `via.Expose` (A8), not
   `WithExposedActions` (C16/E) — one escape hatch, one name; the
   A/B/C/D majority over E's spelling.
6. E11's per-row guide documents A22's `ref.With` pattern, not E's
   forwarder-pattern fallback — the settled dispatch synthesis
   includes A22, so the fallback text would document a footgun in
   its obituary week. README apology deleted in P2.3's PR.
7. E14's wrapper-pattern tutorial cut (A's reasoning); the page
   documents Key[T] and the store recipe instead.
8. Snippet-gate mechanism: E5's in-place extractor over A19's
   include files and B17/C14's Example funcs (E's merge, B/C/D
   concurring) — docs stay plain Markdown.
9. Stricter-verify selections where amendments differed: C3 takes
   the union of all three amendments; B15 keyed by SHA-256/exact
   string (C) over fnv1a; A18 verified on embedded JetStream in
   default CI (D) over Docker integration tags; E12's manual IA
   review replaced by the greppable fold-marker check (E); C14's
   "adjacent to integrity" grep replaced by fenced-block-or-allowlist
   (E); D6's "affirmative safety sentence" judgment replaced by the
   proof-marker registry convention (E).
10. Item-count consolidations (content fully preserved): E21 folded
    into P2.11; C18 into P1.13; B18 into P1.7; A4 into P1.9; D5 into
    P0.9; D4 into P0.10; D14 into P2.7; E10/E13 into P2.12; D15/D16
    into P3.7; E17/E18 into P4.5; B16/E16 into P4.3.
11. B5 phase: P1 with the A/C/D majority over B/E's P0 — P0.7's
    resync fallback is the P0 correctness half; bounding is DoS
    hardening, not silent corruption.
12. E16's EXPERIMENTAL banner extracted into P0.12 (P0-cheap per A);
    the full author kit stays P4.
13. Loopback cookies apply both amendments: detection keys off the
    bound listener address only (C) and the regression runs in the
    browser harness (B).
14. The uploads doc rewrite covers the P1.4 surface at P2; the P3.4
    chunked surface is documented as a same-PR addendum when it
    lands — the page never describes an API that does not exist.
15. Backplane v1 endgame: both branches pre-authorized — in-root if
    P3.8's gate held through the soak window, otherwise the module
    split or option gate per P4.1. No relitigation at tag time.
16. Coverage gap repaired: consensus blocker 13's "Fragment drops
    attributes" had no owning item in any of the 91; the chair folded
    a construction-time panic fix into P0.11 with a named test.
17. The hermetic no-egress CI job moves from A20 (P4) to P1.13 with
    C15, per A's own amendment and B's concurrence — the lock-in for
    P0.4 must not wait eleven items.
18. E7's CSP doc text splits: the default-policy section lands inside
    P0.5's same-PR docs (B's one-PR-not-two amendment); the broader
    security checklist lands in P1.12.

## Ratification record

Ratified unanimously (5-0) by the adversarial review panel on tick R4:

- Panelist A (Go API design & DX): RATIFY, 4 non-blocking notes
- Panelist B (hypermedia/frontend): RATIFY, 10 non-blocking notes
- Panelist C (application security): RATIFY, 3 non-blocking notes
- Panelist D (distributed systems/SRE): RATIFY, 4 non-blocking notes
- Panelist E (usability/docs): RATIFY, 8 non-blocking notes

No blocking amendments were filed. Non-blocking notes are recorded in
rm4-*.md and may be folded in during item grooming. Process: tick 1
independent drafts (91 items) -> tick 2 cross-critique (3 conflict
votes, ~30 merge directives) -> tick 3 chair consolidation (50 items,
18 rulings) -> tick 4 unanimous ratification.
