package via

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-via/via/internal/hcore"
)

// actionMode is decided by the request, not the route: the bundled Datastar
// client sends Datastar-Request: true on every @post; a native <form> submit
// never does, so it always gets a full-page re-render instead of an
// element-patch.
type actionMode int

const (
	modeDatastar actionMode = iota
	modeNative
)

// tabSignal is the wire name of the per-connection tab id — the CSRF token in
// this project's threat model, and the canonical home of the no-underscore
// rule that writeSignalsFrame and writeHTMLPage defer to.
//
// THE NAME MUST NOT START WITH "_". Datastar's default signal filter excludes
// /(^|\.)_/, so an underscored name is never posted back — and every action
// POST depends on this one riding along in the signal store Datastar already
// ships; the ordinary name IS the mechanism.
//
// As a signal it sits in the request body, is set by same-origin JS, and is
// never auto-attached by the browser — a synchronizer token, which is what a
// CSRF token must be. A user signal may not take this name (declareSignal
// panics).
const tabSignal = "viatab"

// tabFormField carries the tab id for a native <form> submit, which ships
// neither Datastar's signal store nor its headers. Subject to the same
// per-mount ownership check as the signal (see dispatch) — only where the id
// is read from changes.
const tabFormField = "_viatab"

// warnNoChange breaks the silence of the commonest week-one defect: the handler
// mutated a store, its unit's OnInit had already loaded the PRE-action data,
// the re-render is therefore byte-identical, and via answers 204 — a click that
// does nothing, with nothing in the log to say why.
//
// Narrowed to the exact shape that produces the defect — a unit that LOADS in
// OnInit and never re-reads — so an idempotent action on a unit with no OnInit,
// or on one that already declares OnReload, stays silent. Deduped per action per
// process: a legitimately idempotent click is a dead click every time it is
// made, and one line per click buries the log instead of reading it.
func (m *mount) warnNoChange(act, name string, v any) {
	if _, isReloader := v.(Reloader); isReloader {
		return
	}
	if _, isIniter := v.(Initer); !isIniter {
		return
	}
	if _, dup := m.noChange.LoadOrStore(act+"\x00"+name, struct{}{}); dup {
		return
	}
	log.Printf("via: action %s (%s) changed nothing the render shows, so it answers 204 and the UI "+
		"does not move. If it mutated data this unit loads in OnInit, that data is stale by now: "+
		"re-read it in an OnReload(*via.Ctx) error method, which via runs after every action on this unit",
		act, name)
}

// warnAtCapacity breaks the silence of a router-wide refusal. The cap is
// router-wide with no per-IP share, so one client CAN fill it and lock every
// other tab out; a per-IP cap is a 1.0 change, but the operator at least has to
// be told which wall was hit and which knob moves it.
//
// Rate-limited to one line a minute rather than deduped for the life of the
// process: unlike a wiring mistake, being at capacity comes and goes, and a
// refusal storm would otherwise be the loudest thing in the log.
func (m *mount) warnAtCapacity(open int64) {
	now := time.Now().UnixNano()
	last := m.capWarn.Load()
	if now-last < int64(time.Minute) || !m.capWarn.CompareAndSwap(last, now) {
		return
	}
	log.Printf("via: refusing an SSE connect with 503: %d of %d live streams already open for this router. "+
		"There is no per-IP share of that cap, so a single client can hold all of it; every tab is refused "+
		"until one closes. The limit is the maxSSEConn constant — raise it only with the memory to back it.",
		open, m.maxLive)
}

// actionResult is what running an action produced. On the live path it all
// happens on the connection's own goroutine, so every outcome has to be
// carried back across the channel rather than answered where it occurred.
type actionResult struct {
	redirect  string
	panicked  bool
	badArg    error  // ?a= failed to decode (badActionArg) — 400, not 500
	pushWork  func() // live only: the dirty-signals + element push, run by tabStream.run right after acking
	gone      string // live only: the unit/action lookup came up invalid; the reason is the response body
	initErr   error  // live only: the post-action OnInit re-run failed (see reloadUnit)
	forbidden string // live only: the session-bound check rejected the request
	paramMiss bool   // live only: a Param segment did not decode — 404, as on the plain path
	// unavailable is a 503: the request was well-formed and the tab is live,
	// but a dependency (the session store) could not answer. Distinct from
	// forbidden so a store blip stops being reported as a security rejection.
	unavailable string
}

// mount bundles a page's per-request wiring, shared by its GET, action, and
// SSE routes so origin floor, OnInit, and body caps can't drift between them.
type mount struct {
	cfg         *config
	sessions    *sessionManager
	reg         *registry
	newInst     func() instance
	patternBase string
	names       []string
	liveCount   *atomic.Int64 // concurrent SSE streams across the whole router, capped at maxLive
	maxLive     int
	noChange    *sync.Map     // the Router's dead-click warning dedupe (see warnNoChange, unknownAction)
	capWarn     *atomic.Int64 // unix nanos of the last at-capacity log, router-wide (see warnAtCapacity)
	// routerCtx bounds every stream this mount opens, so Router.Close can end
	// them; live counts the streams still running, so Close can wait.
	routerCtx context.Context
	live      *sync.WaitGroup
}

// unit returns the bind pass's unit Ctx for dispatch address embed: "r" is the
// root, anything else a '-'-joined ordinal path down the embed tree ("0-1" is
// the root's first Embed's second Embed).
func (bind *Ctx) unit(embed string) *Ctx {
	if embed == rootAddr {
		return bind
	}
	cur := bind
	for _, seg := range strings.Split(embed, "-") {
		k, err := strconv.Atoi(seg)
		if err != nil || k < 0 || k >= len(cur.embeds) {
			return nil
		}
		cur = cur.embeds[k]
	}
	return cur
}

// unit returns the connected live unit for dispatch address embed. Called on
// the embed goroutine (see dispatchOverStream) so the lookup is atomic with the
// dispatch it guards; it still takes replace's lock, so a future caller reading
// it from elsewhere need not remember to add one.
func (c *tabStream) unit(embed string) *Ctx {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.units[embed]
}

// rootAddr is a letter precisely so it cannot collide with an embed key, which
// is always a '-'-joined path of ordinals.
const rootAddr = "r"

// unitAddr is c's own dispatch address in /_via/a/{embed}/{act}.
func unitAddr(c *Ctx) string {
	if c != nil && c.isEmbed {
		return c.embedKey
	}
	return rootAddr
}

// dispatch is the single entry point for every action POST on a mount, at
// {base}/_via/a/{embed}/{act}. One origin floor, one OnInit, one body decode:
// the six transports this replaced each re-implemented them and drifted (OnInit
// never ran on an embed action; a live/embed action dropped ctx.Redirect).
func (m *mount) dispatch(w http.ResponseWriter, req *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			recoverToHTTP(w, req, rec, "action")
		}
	}()
	if !originAllowed(req, m.cfg) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	mode := modeNative
	if req.Header.Get("Datastar-Request") == "true" {
		mode = modeDatastar
	}
	in, ok := decodeSignals(w, req, mode)
	if !ok {
		return
	}
	if mode == modeNative {
		defer req.MultipartForm.RemoveAll() // drop any spilled temp files
	}
	embed := req.PathValue("embed")
	act := req.PathValue("act")
	base := concreteBase(m.patternBase, req, m.names)

	tab := ""
	if mode == modeNative {
		tab = req.PostFormValue(tabFormField)
	} else if raw, ok := in[tabSignal]; ok {
		// Anything that is not a JSON string leaves tab empty and falls through
		// to the plain path — the same graceful answer a stale id gets.
		_ = json.Unmarshal(raw, &tab)
	}
	if lc, ok := m.reg.get(tab); ok {
		if lc.mount != m {
			// The registry is router-wide, and a tab id from another mount is
			// otherwise structurally valid here (same field, same action-id
			// space), so nothing else would fail first.
			http.Error(w, "no such embed", http.StatusGone)
			return
		}
		// A streaming page's PLAIN child echoes the tab id too, and its address
		// was never registered on the connection. Membership is stable (a unit
		// is published before the tab id is, and never removed), so this check
		// is safe off the embed goroutine, unlike the staleness lookup
		// dispatchOverStream still does there.
		if lc.unit(embed) != nil {
			m.dispatchOverStream(w, req, mode, lc, embed, act, in, base)
			return
		}
	}
	m.dispatchPlain(w, req, mode, embed, act, in, base, tab)
}

// decodeSignals decodes an action POST's body per mode. A native submit is
// multipart, so the cap rises to maxUploadBytes; only maxActionBody stays in
// RAM, the rest spills to a temp file the caller removes.
func decodeSignals(w http.ResponseWriter, req *http.Request, mode actionMode) (map[string]json.RawMessage, bool) {
	if mode == modeNative {
		req.Body = http.MaxBytesReader(w, req.Body, maxUploadBytes)
		if err := req.ParseMultipartForm(maxActionBody); err != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return nil, false
			}
			http.Error(w, "malformed form", http.StatusBadRequest)
			return nil, false
		}
		return nil, true
	}
	in := map[string]json.RawMessage{}
	dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxActionBody))
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return nil, false
		}
		http.Error(w, "malformed request body", http.StatusBadRequest)
		return nil, false
	}
	return in, true
}

// dispatchOverStream runs act against a connected live unit on its
// connection's serialized goroutine and WAITS for the result — synchronous,
// unlike the old fire-and-forget embed dispatch, so a Redirect, the session
// cookie, and a panic all resolve on THIS response like a plain action. The
// wait is bounded by req.Context() as well as the connection closing, so a
// stalled peer elsewhere can't park this POST's goroutine forever.
func (m *mount) dispatchOverStream(w http.ResponseWriter, req *http.Request, mode actionMode, lc *tabStream, embed string, act string, in map[string]json.RawMessage, base string) {
	res, outcome := lc.run(req.Context(), func() actionResult {
		// A queued closure runs regardless of what its caller did meanwhile: if
		// req.Context() is already done, run has given up and answered 410, so
		// applying the action now would double-apply on a client retry, and w
		// is a dead ResponseWriter whose Header() would race the server's
		// post-handler teardown.
		if req.Context().Err() != nil {
			return actionResult{gone: "request abandoned"}
		}
		// Checked here, not before the closure was posted: a cookieless
		// dispatch that passed a pre-queue check while the connection was
		// unbound could be applied AFTER a concurrent live login bound it. Here
		// the compare and the run are atomic on one serialized goroutine.
		if bound := lc.boundSession(); bound != "" {
			// A dispatch must carry the SAME session, by pointer not id (a
			// Rotate moves the pointer to a new id, never a new data object).
			// Otherwise a leaked tab id is a bearer credential good from any
			// request, session or none, once the origin floor is open.
			// The store error is kept, not discarded: a store that could not
			// answer yields s == nil, which used to read as "wrong session"
			// and answer 403. A blip is not a rejection, and the developer got
			// only sess.go's "Load failed" with nothing tying it to the 403.
			_, s, err := m.sessions.resolve(req)
			if err != nil {
				return actionResult{unavailable: "session store unavailable"}
			}
			if s == nil || s.sid != bound {
				return actionResult{forbidden: "session mismatch"}
			}
		}
		// Also looked up here, not before the closure was posted: a concurrent
		// push replaces lc.units, so a unit read earlier could be stale. That
		// is more than an address mismatch — Signal.bind stamps the dirty sink
		// at render time, so acting against an older unit attributes the write
		// to a Ctx nothing downstream reads, silently dropping the patch.
		u := lc.unit(embed)
		if u == nil {
			return actionResult{gone: "no such embed"}
		}
		a, ok := u.actions[act]
		if !ok {
			return actionResult{gone: m.unknownAction(u, act)}
		}
		return liveRunAction(w, req, m.sessions, lc, u, in, a)
	})
	switch outcome {
	case runPinned:
		// 503, not 410: the tab is fine and the action is legitimate — this
		// connection's goroutine is simply not answering. See warnPinned.
		http.Error(w, "stream busy", http.StatusServiceUnavailable)
		return
	case runClosed:
		http.Error(w, "stream closed", http.StatusGone)
		return
	case runAbandoned:
		http.Error(w, "request abandoned", http.StatusGone)
		return
	}
	if res.unavailable != "" {
		http.Error(w, res.unavailable, http.StatusServiceUnavailable)
		return
	}
	if res.forbidden != "" {
		http.Error(w, res.forbidden, http.StatusForbidden)
		return
	}
	if res.gone != "" {
		http.Error(w, res.gone, http.StatusGone)
		return
	}
	if res.initErr != nil {
		answerReloadFailure(w, res.initErr)
		return
	}
	if res.badArg != nil {
		http.Error(w, "bad action arg: "+res.badArg.Error(), http.StatusBadRequest)
		return
	}
	if res.paramMiss {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if res.panicked {
		http.Error(w, "action failed", http.StatusInternalServerError)
		return
	}
	if mode == modeNative {
		respond(w, req, mode, res.redirect, func() {
			// A native submit replaces the whole document, so this must be the
			// page a brand-new connection will hold — a fresh instance with
			// OnInit run, not a snapshot of the dying connection's live tree
			// (which the reconnect is about to reseed anyway).
			m.writePage(w, req, m.newInst(), base, nil)
		}, nil)
		return
	}
	respond(w, req, mode, res.redirect, nil, nil) // patch: nil — the push already framed it
}

// liveRunAction hydrates unit's signals from in and runs act. unit is the bind
// Ctx the last push produced, whose actions/hydrators table is current because
// every push is a render — so nothing re-renders here first. It runs on the
// connection's serialized goroutine while the POST blocks in tabStream.run, so
// w is safe to write to exactly as on a plain action.
//
// The re-render + SSE push rides back in res.pushWork instead of running here:
// run sends the result to the waiting POST FIRST, so a stalled peer's write
// delays only the NEXT push item, never this response. A detached goroutine
// doing the enqueue used to race other actions' goroutines and reorder their
// pushes; returning it as data keeps everything on the one goroutine, in order.
func liveRunAction(w http.ResponseWriter, req *http.Request, sessions *sessionManager, lc *tabStream, unit *Ctx, in map[string]json.RawMessage, act action) (res actionResult) {
	defer func() {
		if rec := recover(); rec != nil {
			if bad, ok := rec.(badActionArg); ok {
				res = actionResult{badArg: bad.err}
				return
			}
			if un, ok := rec.(unrenderedArg); ok {
				res = actionResult{gone: un.body()}
				return
			}
			// Same sentinel, same answer as recoverToHTTP gives the plain path:
			// the URL names something that does not exist, which is a 404, not
			// a server fault worth a stack dump.
			if _, ok := rec.(paramMiss); ok {
				res = actionResult{paramMiss: true}
				return
			}
			log.Printf("via: live action panic [tab=%s unit=%T act=%s]: %v\n%s",
				lc.id, unit.embedV.v, act.name, rec, debug.Stack())
			res = actionResult{panicked: true}
		}
	}()
	// Registered BEFORE the hydrate loop so EVERY exit unwinds it, not just the
	// one that reaches a push: a malformed arg, an unrendered arg, a paramMiss,
	// a handler panic or a reloadUnit error all return without pushWork, and
	// livePush's restore is the only other one — so without this the client's
	// posted values stayed on the instance for the next Tick/Listen handler to
	// read. Deferred rather than inline so the handler and reloadUnit still see
	// the hydrated values; a Set inside the handler drops its own slot, so a
	// server write still survives.
	defer lc.rev.restore()
	for slot, raw := range in {
		if hydrate, ok := unit.hydrators[slot]; ok {
			hydrate(raw)
			// Remembered so the next push's DISPLAY render still shows what the
			// client is holding: livePush reverts the instance to its
			// server-authored values before the authority render, and without
			// this the client's value would vanish from the frame that follows
			// its own action. It never reaches the authority render (I1).
			lc.client[slot] = raw
		}
	}
	// A fresh Ctx per dispatch, not unit itself: a Tick/Listen handler holds
	// unit for the life of the connection, so writing req/sessW/redirect onto
	// it would rewrite what that handler sees. Signal.Set and State.Set still
	// land on unit — those handles were bound to it at render time.
	//
	// beforeSession is resolved BEFORE the action runs, not inferred from
	// "rc.session == nil after": Ctx.Session() lazily resolves the same
	// request's cookie whether the action reads or writes, so a request already
	// carrying a valid (e.g. an attacker's) cookie would look post-hoc
	// identical to one that just minted a session, binding the connection to a
	// session the action never created (I1).
	_, beforeSession, _ := sessions.resolve(req)
	rc := &Ctx{req: req, sessions: sessions, sessW: w}
	// unit.dirty is NOT reset here: clearDirty (via flushDirty, in the actual
	// push) is the only place that owns clearing it. Resetting unconditionally
	// on every dispatch dropped an earlier action's Set the moment it panicked
	// or reloadUnit errored — no push ran to flush it, and this line wiped it
	// before the next action's push ever saw it.
	act.fn(rc)

	// Re-load the unit the same way the plain path does: the handler mutated
	// state this unit's OnInit had already read, and the push render below
	// would otherwise frame the pre-action data. Skipped behind a Redirect —
	// the tab is navigating away from this render. See Reloader.
	if rc.redirect == "" {
		rl := &Ctx{req: req, sessions: sessions, sessW: w, session: rc.Session(), base: unit.base}
		if err := reloadUnit(unit.embedV.v, rl); err != nil {
			return actionResult{initErr: err}
		}
		rc.redirect = rl.redirect
	}

	// A session minted on a connection that was anonymous at connect: bind it
	// now so the tab id stops being a bearer credential the instant this action
	// logs it in (H1; bindSession is a no-op once bound).
	if beforeSession == nil && rc.session != nil && rc.session.data != nil {
		lc.bindSession(rc.session.sid())
	}

	// The dirty set this action wrote is shipped by the push itself
	// (tabStream.flushDirty), on the one path every server-driven signal change
	// takes — a Tick/Listen handler's Set included.
	push := unit.push
	return actionResult{
		redirect: rc.redirect,
		pushWork: push, // patch-signals, then re-render this unit and frame the element-patch
	}
}

// unknownAction is the 410 body for an id this render does not bind — a branch
// closed, or (the common wiring mistake) an OnInit that failed to restore the
// UI state the View branches on. The bound list goes to the log only: it is a
// map of the render's Go type and method names.
//
// Deduped per unknown id per process, like warnNoChange: the line dumps the
// whole action table, so an unauthenticated POST loop over garbage ids would
// otherwise print the render's method names at line rate — a log-flood
// amplifier and a disclosure channel in one.
func (m *mount) unknownAction(u *Ctx, act string) string {
	if _, dup := m.noChange.LoadOrStore("unknownAction\x00"+act, struct{}{}); !dup {
		have := make([]string, 0, len(u.actions))
		for id, a := range u.actions {
			have = append(have, id+" ("+a.name+")")
		}
		sort.Strings(have)
		log.Printf("via: no such action %s; this render binds: %s", act, strings.Join(have, ", "))
	}
	return "no such action " + act + "; this render does not bind it"
}

// noStream explains a live unit's action arriving on the plain path. The ways
// to get here are different failures, and the old single message ("the page was
// served plain") asserted the one that is usually false — what actually went
// missing is the tab id that routes the POST back to its connection.
func noStream(mode actionMode, tab string) string {
	if tab != "" {
		return "this tab id has no open stream for this page: the connection closed, " +
			"or the id belongs to a page that is gone — reload"
	}
	carrier := tabSignal + " signal"
	if mode == modeNative {
		carrier = tabFormField + " form field (via.PostForm renders it; a hand-written <form> must include it)"
	}
	return "this action needs the page's tab id and the request carried no " + carrier +
		": the page never opened its stream, or the id was filtered out"
}

// writePage writes a full HTML document for inst — the GET, and the full-page
// re-render a native <form> submit answers with. Liveness is read off THIS
// render rather than assumed: a plain root may carry a live EMBED, and
// hard-coding "not live" shipped that page with no data-init, leaving the embed
// dead after the first form submit.
//
// from non-nil already ran OnInit for this request; it carries that wiring down
// (inheritRequestScope) instead of running OnInit twice.
func (m *mount) writePage(w http.ResponseWriter, req *http.Request, inst instance, base string, from *Ctx) {
	ctx, body := inst.renderPage(w, req, m, base, from)
	if ctx == nil {
		return
	}
	writeHTMLPage(w, m.cfg, body, base, len(liveUnits(ctx)) > 0, ctx.pageHead())
}

func (inst instance) renderPage(w http.ResponseWriter, req *http.Request, m *mount, base string, from *Ctx) (*Ctx, []byte) {
	if from != nil {
		return renderRootBase(inst, true, base, nil, nil, from, nil)
	}
	ctx := newRootCtx(true, base, nil)
	ctx.embedV = inst // the root is a unit like any embed, when it is live
	if runOnInit(inst.v, ctx, w, req, m.sessions) != nil {
		return nil, nil
	}
	return ctx, renderRootWith(ctx, inst.v)
}

// dispatchPlain binds a fresh instance, runs OnInit, runs the acted-on unit's
// action, then answers per mode.
func (m *mount) dispatchPlain(w http.ResponseWriter, req *http.Request, mode actionMode, embed string, act string, in map[string]json.RawMessage, base string, tab string) {
	inst := m.newInst()
	// Discovery is two-phase. auth is the render the client did not influence:
	// it alone decides what is dispatchable (see OnArg). Then the body is
	// applied to the slots that render made client-writable and the tree is
	// re-rendered, so a Bind()ed signal opening a lazy branch gets the slots
	// inside it hydrated too — without this their posted values were silently
	// dropped. The action must be present in BOTH, so the executed render is an
	// intersection with auth, never a superset.
	auth := newRootCtx(true, base, map[string]any{}) // nil only would read as "declare everything"
	auth.embedV = inst                               // so auth.unit(rootAddr)'s liveness reads the same way an embed's does
	if runOnInit(inst.v, auth, w, req, m.sessions) != nil {
		return
	}
	rootBefore := renderRootWith(auth, inst.v)
	bind := auth
	done := map[string]bool{}
	n := 0
	for ; n < maxHydratePasses && hydrateTree(bind, in, done); n++ {
		bind = rebindFrom(auth)
		rootBefore = renderRootWith(bind, inst.v)
	}
	if n == maxHydratePasses {
		// Never expected: done grows monotonically, so a View would have to
		// bind a fresh slot on every pass. Loud, because the alternative is a
		// silently half-hydrated render.
		log.Printf("via: plain discovery hit the %d-pass cap for action %s; some posted signals may be unapplied", maxHydratePasses, act)
	}
	ua := auth.unit(embed)
	if ua == nil {
		http.Error(w, "no such embed", http.StatusGone)
		return
	}
	authAct, ok := ua.actions[act]
	if !ok {
		http.Error(w, m.unknownAction(ua, act), http.StatusGone)
		return
	}
	u := bind.unit(embed)
	if u == nil {
		http.Error(w, "no such embed", http.StatusGone)
		return
	}
	// Same guard, same reason, as the acted-instance substitution in
	// embedViewer: a When around an Embed that depends on a hydrated signal can
	// shift ordinals, so one key may denote different types in the auth and
	// bind renders. Embed's docs forbid such a When; fail closed rather than
	// run the handler against a unit the authorization never looked at.
	if ua.embedV.typ != u.embedV.typ {
		http.Error(w, "no such embed", http.StatusGone)
		return
	}
	a, ok := u.actions[act]
	if !ok {
		http.Error(w, m.unknownAction(u, act), http.StatusGone)
		return
	}
	// The intersection is per (handler, ARG), not per handler: a posted signal
	// that widens an Each would otherwise widen the accepted arg set too, and
	// an arg only the client's own body produced would dispatch. a.args is the
	// very map the slot's closure checks against, so pruning it in place is
	// what fails the unauthorized arg closed.
	for k := range a.args {
		if _, ok := authAct.args[k]; !ok {
			delete(a.args, k)
		}
	}
	if ua.live {
		// Liveness is read off the AUTH render, never off bind (I1/I5): only
		// auth ran the root's OnInit. Reaching here means the tab was missing
		// or stale, so fail closed rather than mutating a throwaway instance.
		http.Error(w, noStream(mode, tab), http.StatusGone)
		return
	}
	u.req = req
	u.sessions = m.sessions
	u.sessW = w
	a.fn(u) // no long-lived handler holds this render's Ctx, so u is its own dispatch Ctx

	// The handler mutated state the acted unit's OnInit had already read, so
	// the response render below would frame the PRE-action data — a 204 and a
	// silently unchanged UI. Skipped behind a Redirect: nothing from this
	// instance gets rendered. See Reloader.
	if u.redirect == "" {
		rl := &Ctx{req: req, sessions: m.sessions, sessW: w, session: auth.session, base: base}
		if err := reloadUnit(actedViewer(inst, u), rl); err != nil {
			answerReloadFailure(w, err)
			return
		}
		u.redirect = rl.redirect
	}

	if mode == modeNative {
		respond(w, req, mode, u.redirect, func() {
			m.writePage(w, req, inst, base, u)
		}, nil)
		return
	}
	respond(w, req, mode, u.redirect, nil, func() []byte {
		b := m.rerenderPlain(embed, rootBefore, inst, bind, u, base)
		if b == nil {
			m.warnNoChange(act, a.name, actedViewer(inst, u))
		}
		return b
	})
}

// actedViewer is the composition the action just mutated: the root, or the
// embed instance the discovery render bound. Re-loading the ROOT after an embed
// action would reload the wrong unit and leave the acted one stale.
func actedViewer(inst instance, u *Ctx) any {
	if u.isEmbed {
		return u.embedV.v
	}
	return inst.v
}

// rebindFrom builds the Ctx for hydration pass >= 2: a CLONE of the auth
// render with only the per-render tables reset. Three separate defect rounds
// were one field set on auth and forgotten here (req/sessions/sessW/doInit,
// then session, then live/initDone), each patched by hand-copying one more
// field — so the default is inverted: a new Ctx field rides along unless it is
// reset below, and forgetting one can no longer silently drop a request scope.
//
// Reset are exactly the things a render PRODUCES and the next render must
// produce again (slot order/initials, the action and hydrator tables, the
// embed tree, the rendered bytes and push closure, this dispatch's dirty set
// and redirect), plus the root's OnInit registrations — OnInit does not re-run
// here and nothing on the plain path consumes them. `live` is NOT reset: it is
// auth's verdict, and a later pass may only widen it (I5).
//
// The invariants this loop must preserve:
//
//	I1 the authority for actions, args AND liveness is the un-hydrated auth render.
//	I2 passes >= 2 widen only hydratable slots; the executed action is auth ∩ bind
//	   per (handler, arg).
//	I3 one request = one *Session, resolved by runOnInit and propagated by copy;
//	   only Session.ensure mints.
//	I4 a Session.w is non-nil iff its response is still writable.
//	I5 liveness is a GET/auth verdict — a bind pass cannot lower it and a
//	   re-render cannot raise it.
func rebindFrom(auth *Ctx) *Ctx {
	c := *auth
	c.order, c.initial = nil, map[string]any{}
	c.actions = map[string]action{}
	c.hydrators = map[string]func(json.RawMessage){}
	c.dirty = map[string]any{}
	c.embeds = nil
	c.rendered, c.push = nil, nil
	c.redirect = ""
	c.ticks, c.subs, c.onConnect, c.disposers = nil, nil, nil, nil
	return &c
}

// hydrateTree applies the POST body's signals to every slot the discovery
// render bound, page-wide, AFTER that render finished. It records each slot it
// reached in done and reports whether it reached a new one, which is
// dispatchPlain's signal that another render may uncover more.
//
// Deliberately not done DURING the render, which is what decides what is
// dispatchable (see OnArg): hydrating from the body let a client flip a Signal
// OnInit set from the session — via.When(p.Admin.Get(), …) opened by posting
// {"admin":true} — and mint its own authorization. Later passes DO see hydrated
// values, but only to widen the set of hydratable slots; the action table they
// may dispatch from stays auth's.
func hydrateTree(c *Ctx, in map[string]json.RawMessage, done map[string]bool) bool {
	fresh := false
	for slot, raw := range in {
		hydrate, ok := c.hydrators[slot]
		if !ok {
			continue
		}
		hydrate(raw)
		if !done[slot] {
			done[slot] = true
			fresh = true
		}
	}
	for _, child := range c.embeds {
		if hydrateTree(child, in, done) {
			fresh = true
		}
	}
	return fresh
}

// maxHydratePasses bounds dispatchPlain's discovery loop. The loop cannot
// oscillate — done only grows, and is bounded by |in ∩ hydrators| — so the cap
// is a backstop against a pathological View that binds a brand-new slot name on
// every render, not against divergence. A page whose body carries any Bind()ed
// slot takes exactly 2 passes (2 renders), which is the floor: the handler has
// to run on a render that already saw the posted values.
//
// Each pass re-renders the whole tree, so an embedded child's OnInit runs once
// per pass — twice for the common case. Keep OnInit cheap and idempotent.
const maxHydratePasses = 8

// rerenderPlain re-renders the acted-on unit for a Datastar action's response,
// restricted to the signals the action wrote. A plain embed gets its own
// container plus a data-signals attribute — the piece the old embed-action
// handler omitted, silently dropping a Signal.Set inside a plain embed's
// action. Returns nil when unchanged (→ 204).
func (m *mount) rerenderPlain(embed string, rootBefore []byte, inst instance, bind, u *Ctx, base string) []byte {
	seen := bind.slotSet()
	if embed == rootAddr {
		// data-signals is a plain action's only channel for a server-side Set,
		// restricted to what it wrote since re-declaring every slot would
		// clobber a value the user is mid-edit. dirtyAll never returns nil,
		// which would read as "declare everything".
		only := bind.dirtyAll()
		afterCtx, after := renderRootBase(inst, true, base, only, seen, u, nil)
		if len(liveUnits(bind)) == 0 {
			assertRenderInvariantLiveness(len(liveUnits(afterCtx)) > 0)
		}
		if bytes.Equal(rootBefore, after) {
			return nil
		}
		return after
	}
	afterCtx, afterInner := renderEmbedBind(u.embedKey, u.embedV, base, u, nil)
	assertRenderInvariantLiveness(afterCtx.live)
	if bytes.Equal(u.rendered, afterInner) && len(u.dirty) == 0 {
		return nil
	}
	var buf bytes.Buffer
	buf.WriteString(`<div id="via-i` + u.embedKey + `"`)
	writeSignalsAttr(&buf, afterCtx.order, afterCtx.initial, u.dirty, seen)
	buf.WriteString(`>`)
	buf.Write(afterInner)
	buf.WriteString(`</div>`)
	return buf.Bytes()
}

// pruneToAuthority intersects a live DISPLAY render's action table with the
// authority render's, per handler AND per arg — the same intersection
// dispatchPlain does inline, for the same reason: a posted signal may widen
// what the client sees and must never widen what it may call.
//
// Walked by embed ordinal, since that is the dispatch address. A type mismatch
// at a key (a When around an Embed that a hydrated signal shifted) fails the
// whole subtree closed rather than authorizing against a unit the authority
// render never looked at.
func pruneToAuthority(bind, auth *Ctx) {
	if auth == nil || bind.embedV.typ != auth.embedV.typ {
		clear(bind.actions)
		for _, child := range bind.embeds {
			pruneToAuthority(child, nil)
		}
		return
	}
	for id, a := range bind.actions {
		authAct, ok := auth.actions[id]
		if !ok {
			delete(bind.actions, id)
			continue
		}
		for k := range a.args {
			if _, ok := authAct.args[k]; !ok {
				delete(a.args, k)
			}
		}
	}
	for i, child := range bind.embeds {
		var ac *Ctx
		if i < len(auth.embeds) {
			ac = auth.embeds[i]
		}
		pruneToAuthority(child, ac)
	}
}

// assertRenderInvariantLiveness fails an action that turned a unit live on a
// page served plain. Only the GET's verdict bootstraps an SSE stream, so a
// State reached through a branch CLOSED at GET leaves the tab demanding a
// connection it never opened, and every action after it 410s — a frozen tab
// with no clue why. It panics rather than logging on: the failure is
// deterministic, and the alternative is a tab already dead that doesn't say so.
func assertRenderInvariantLiveness(nowLive bool) {
	if !nowLive {
		return
	}
	panic("via: this action made a unit live on a page that was served plain — the tab has no stream. " +
		"Every action after this one would 410. Liveness must not depend on a branch an action can " +
		"open: render the State unconditionally, or register a Tick/Listen in OnInit so the page is " +
		"live from the first render")
}

// redirectInit navigates the tab from a Datastar @post action's response.
//
// Datastar v1.0.2 answers a text/javascript response by building a <script>,
// copying the datastar-script-attributes header's JSON onto it as attributes,
// setting its textContent to the body and appending it to document.head (see
// the client's Content-Type switch, next to its text/html and application/json
// branches). So the TARGET rides in an attribute and THESE BYTES NEVER CHANGE —
// which is what lets the strict CSP admit the script by SHA-256, exactly like
// reconnectInit, with no per-response nonce to mint or leak. Edit this string
// and buildCSP re-derives the hash, but the two must stay byte-identical or the
// browser drops the script and the redirect is silently dead again.
const redirectInit = `(()=>{var s=document.currentScript;if(!s)return;` +
	`var u=s.getAttribute('data-via-to');s.remove();if(u)location.assign(u)})()`

// writeRedirectScript answers a @post with the navigation script. The target is
// JSON-encoded into a header and reaches the DOM through setAttribute, never
// through the HTML parser, and respond has already cleared it through
// hcore.SafeURL — so no javascript: target and no attribute escape.
func writeRedirectScript(w http.ResponseWriter, target string) {
	attrs, err := json.Marshal(map[string]string{"data-via-to": target})
	if err != nil {
		http.Error(w, "redirect failed", http.StatusInternalServerError)
		return
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "text/javascript; charset=utf-8")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Datastar-Script-Attributes", string(attrs))
	w.Write([]byte(redirectInit))
}

// respond is dispatch's one response policy for every action POST — root,
// embed, live, or native form. A queued Redirect wins: a native submit gets a
// 303, a Datastar @post gets the navigation script above. Either way the target
// must clear hcore.SafeURL first — the same URL policy runOnInit and rendered
// href/src URLs use — and an unsafe one is dropped, not followed. Otherwise
// renderNative or renderPatch (nil for "unchanged" / "the live push already
// carried it") decides the body.
func respond(w http.ResponseWriter, req *http.Request, mode actionMode, redirect string, renderNative func(), renderPatch func() []byte) {
	if redirect != "" {
		switch {
		case !hcore.SafeURL(redirect):
			log.Printf("via: unsafe Redirect target %q dropped", redirect)
		case mode == modeNative:
			http.Redirect(w, req, redirect, http.StatusSeeOther)
			return
		default:
			writeRedirectScript(w, redirect)
			return
		}
	}
	if mode == modeNative {
		renderNative()
		return
	}
	if renderPatch == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	b := renderPatch()
	if b == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Element-patch fragments take the default policy: a fragment carries no
	// document, so its header is inert — only the page's policy governs what
	// runs.
	hdr := w.Header()
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Security-Policy", cspHeader)
	w.Write(b)
}
