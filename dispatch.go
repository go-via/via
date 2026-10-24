package via

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

func isDatastarRequest(req *http.Request) bool {
	return req.Header.Get("Datastar-Request") == "true"
}

// tabSignal is the wire name of the tab id, which an action on a live unit
// must carry to reach its instance; a plain action never checks it, so only
// the origin floor guards those. It is also the canonical home of the
// no-underscore rule that writeSignalsFrame and writeHTMLPage defer to. A live
// page renders it, and its stream adopts it (see newTabID).
//
// THE NAME MUST NOT START WITH "_". Datastar's default signal filter excludes
// /(^|\.)_/, so an underscored name is never posted back — and every action
// POST depends on this one riding along in the signal store Datastar already
// ships; the ordinary name is the mechanism.
//
// As a signal it sits in the request body, is set by same-origin JS, and is
// never auto-attached by the browser, so a cross-site page cannot supply it
// the way the browser supplies a cookie. A user signal may not take this name
// (declareSignal panics).
const tabSignal = "viatab"

// tabSignals is the signal object that hands a page or stream its tab id. id
// is base64url, so it needs no JSON or attribute escaping.
func tabSignals(id string) string { return `{"` + tabSignal + `":"` + id + `"}` }

// tabFormField carries the tab id for a native <form> submit, which ships
// neither Datastar's signal store nor its headers. Subject to the same
// per-mount ownership check as the signal (see dispatch) — only where the id
// is read from changes.
const tabFormField = "_viatab"

// warnNoChange breaks the silence of the commonest week-one defect: the handler
// mutated a store, its unit's OnInit had already loaded the pre-action data,
// the re-render is therefore byte-identical, and via answers 204 — a click that
// does nothing, with nothing in the log to say why.
//
// Narrowed to the exact shape that produces the defect — a unit that loads in
// OnInit and never re-reads — so an idempotent action on a unit with no OnInit,
// or on one that already declares OnReload, stays silent. Deduped per action per
// Router: a legitimately idempotent click is a dead click every time it is
// made, and one line per click buries the log instead of reading it.
func (m *mount) warnNoChange(act, name string, v any) {
	if _, isReloader := v.(reloader); isReloader {
		return
	}
	if _, isIniter := v.(initer); !isIniter {
		return
	}
	if _, dup := m.noChange.LoadOrStore(act+"\x00"+name, struct{}{}); dup {
		return
	}
	m.cfg.log.Warn("via: this action changed nothing the render shows, so it answers 204 and the UI "+
		"does not move. If it mutated data this unit loads in OnInit, that data is stale by now: "+
		"re-read it in an OnReload(*via.Ctx) error method, which via runs after every action on this unit",
		"act", act, "name", name)
}

// perMinute admits one log line a minute across goroutines.
type perMinute struct{ last atomic.Int64 } // unix nanos of the last line admitted

func (p *perMinute) allow() bool {
	now := time.Now().UnixNano()
	last := p.last.Load()
	return now-last >= int64(time.Minute) && p.last.CompareAndSwap(last, now)
}

// warnAtCapacity breaks the silence of a router-wide refusal. The cap is
// router-wide with no per-IP share, so one client can fill it and lock every
// other tab out; a per-IP cap is a 1.0 change, but the operator at least has to
// be told which wall was hit and which knob moves it.
//
// Rate-limited to one line a minute rather than deduped for the life of the
// process: unlike a wiring mistake, being at capacity comes and goes, and a
// refusal storm would otherwise be the loudest thing in the log.
func (m *mount) warnAtCapacity(open int64) {
	if !m.capWarn.allow() {
		return
	}
	m.cfg.log.Warn("via: refusing an SSE connect with 503: the router is at its live-stream cap. "+
		"There is no per-IP share of that cap, so a single client can hold all of it; every tab is refused "+
		"until one closes. The limit is WithMaxSSEConn — raise it only with the memory to back it.",
		"open", open, "cap", m.cfg.maxSSEConn)
}

// warnParkedFull is warnAtCapacity for parked actions: the cap is router-wide
// and a single client can fill it, so the operator is told once a minute.
func (m *mount) warnParkedFull() {
	if !m.reg.parkWarn.allow() {
		return
	}
	m.cfg.log.Warn("via: refusing an action with 503: the router already holds its cap of actions parked "+
		"for a tab whose stream has not connected. There is no per-IP share of that cap. The cap follows "+
		"WithMaxSSEConn, up to "+strconv.Itoa(parkedCap)+".", "cap", m.reg.maxParked)
}

// actionResult is what running an action produced. On the live path it all
// happens on the connection's own goroutine, so every outcome has to be
// carried back across the channel rather than answered where it occurred.
type actionResult struct {
	redirect redirectTo
	fail     *actionFail // nil when the action ran; otherwise the whole answer
	pushWork *unitPusher // live only: the dirty-signals + element push, run by tabStream.run right after acking
	session  *Session    // live only: the action's handle, so a native answer's OnInit sees what the action wrote
}

// actionFail is an action that did not run to a normal answer. It carries its
// own status, so both transports answer a failure the same way.
type actionFail struct {
	status int
	body   string
	note   error // what the error page reads, when it gets a reason
	// hookErr is a failed post-action OnReload, answered like any hook error
	// (see answerHookError) rather than with status and body.
	hookErr error
}

func (f *actionFail) answer(log *slog.Logger, w http.ResponseWriter) {
	if f.hookErr != nil {
		answerHookError(log, w, f.hookErr, "via: OnReload after an action failed")
		return
	}
	if f.note != nil {
		noteErr(w, f.note)
	}
	http.Error(w, f.body, f.status)
}

// failGone is a unit or action lookup that came up invalid; body says why.
func failGone(body string) *actionFail { return &actionFail{status: http.StatusGone, body: body} }

// failPanic is an action that panicked: a 500 whose error the error page reads.
func failPanic(rec any) *actionFail {
	err := fmt.Errorf("via: action panic: %v", rec)
	return &actionFail{status: http.StatusInternalServerError, body: "action failed", note: err}
}

// mount bundles a page's per-request wiring, shared by its GET, action, and
// SSE routes so origin floor, OnInit, and body caps can't drift between them.
type mount struct {
	*routerShared
	root        interface{ newInst() instance }
	patternBase string
	pageSuffix  string // "/" when the page is served at its base plus a slash
	names       []string
	// csp is this mount's Content-Security-Policy, built once at Mount from the
	// router-wide assets plus the root's own PageMeta().Assets; assetsFP is the
	// fingerprint of the latter, re-checked on every document render so a
	// data-dependent Assets cannot silently outrun the policy.
	csp      cspPolicy
	assetsFP string
}

// unit returns the bind pass's unit Ctx for dispatch address child: "r" is the
// root, anything else a '-'-joined ordinal path down the child tree ("0-1" is
// the root's first Child's second Child).
func (bind *Ctx) unit(child string) *Ctx {
	if child == rootAddr {
		return bind
	}
	cur := bind
	for _, seg := range strings.Split(child, "-") {
		k, err := strconv.Atoi(seg)
		if err != nil || k < 0 || k >= len(cur.children) {
			return nil
		}
		cur = cur.children[k]
	}
	return cur
}

// unit returns the connected live unit for dispatch address child. Called on
// the stream goroutine (see dispatchOverStream) so the lookup is atomic with the
// dispatch it guards; it still takes replace's lock, so a future caller reading
// it from elsewhere need not remember to add one.
func (c *tabStream) unit(child string) *Ctx {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.units[child]
}

// liveAncestor reports whether any node from bind down to (but excluding) the
// unit at child is itself live — "every ancestor on the path is plain" is
// Child's own liveness rule, and a plain child dispatched through
// dispatchPlain can still sit beneath an already-live root (a live page's
// plain child never gets its own stream registration, see connectUnit), so
// the re-render at child needs this to seed checkLiveUnderLive correctly.
func liveAncestor(bind *Ctx, child string) bool {
	if child == rootAddr {
		return false
	}
	cur := bind
	segs := strings.Split(child, "-")
	for _, seg := range segs[:len(segs)-1] {
		if cur.live {
			return true
		}
		k, err := strconv.Atoi(seg)
		if err != nil || k < 0 || k >= len(cur.children) {
			return false
		}
		cur = cur.children[k]
	}
	return cur.live
}

// rootAddr is a letter precisely so it cannot collide with a child key, which
// is always a '-'-joined path of ordinals.
const rootAddr = "r"

// validAddr reports whether child has the shape unitAddr produces.
func validAddr(child string) bool {
	if child == rootAddr {
		return true
	}
	for seg := range strings.SplitSeq(child, "-") {
		if seg == "" {
			return false
		}
		for i := range len(seg) {
			if seg[i] < '0' || seg[i] > '9' {
				return false
			}
		}
	}
	return true
}

func unitAddr(c *Ctx) string {
	if c.isChild {
		return c.childKey
	}
	return rootAddr
}

func (m *mount) newInst() instance { return m.root.newInst() }

// serveGet renders the page.
func (m *mount) serveGet(w http.ResponseWriter, req *http.Request) {
	defer m.recoverRequest(w, req, "render")
	// concreteBase, not patternBase: a page at /job/{id} must advertise
	// /job/7/_via/sse. The pattern would be POSTed literally and 404,
	// leaving every live child under a parametrised mount dead.
	base := concreteBase(m.patternBase, req, m.names)
	ctx, body := m.renderFreshPage(w, req, base, nil)
	m.writePage(w, req, ctx, body)
}

// recoverRequest answers a panic on a plain request transport.
func (m *mount) recoverRequest(w http.ResponseWriter, req *http.Request, what string) {
	if rec := recover(); rec != nil {
		m.recoverToHTTP(w, req, rec, what, false)
	}
}

// dispatch is the single entry point for every action POST on a mount, at
// {base}/_via/a/{child}/{act}. One origin floor, one OnInit, one body decode:
// the six transports this replaced each re-implemented them and drifted (OnInit
// never ran on a child action; a live/child action dropped ctx.Redirect).
func (m *mount) dispatch(w http.ResponseWriter, req *http.Request) {
	defer m.recoverRequest(w, req, "action")
	ar, ok := m.readAction(w, req)
	if !ok {
		return
	}
	if ar.mode == modeNative {
		defer req.MultipartForm.RemoveAll() // drop any spilled temp files
	}
	// The tab id rides in the body, so the body is read before anything can
	// park: a parked action holds its decoded signals. parkedCap bounds that.
	deadline := time.Now().Add(m.cfg.pinnedDeadline)
	// A click that beat its stream's connect, or landed in a reconnect gap,
	// may park: the id is one this app issued to this browser, so a stream
	// for it is on its way, and the plain path could only 410 a live unit.
	// An address via never renders is not worth a slot.
	park := ar.tab != "" && validAddr(ar.child) && isActionID(ar.act) && m.issuedTab(ar.tab, tabCredential(m.sessions, req))
	lc, ticket, out := m.reg.enter(req.Context(), m.ctx, ar.tab, deadline, park)
	if m.refuseEnter(w, out) {
		return
	}
	if out == enterReady && m.routeToStream(ar, lc, ticket, deadline) {
		return
	}
	m.dispatchPlain(ar)
}

// actionReq is one action POST, read and decoded: what every step of its
// dispatch works from.
type actionReq struct {
	w     http.ResponseWriter
	req   *http.Request
	mode  actionMode
	child string
	act   string
	in    map[string]json.RawMessage
	base  string
	tab   string
}

// readAction runs the origin floor and decodes the body, answering the request
// itself when either fails.
func (m *mount) readAction(w http.ResponseWriter, req *http.Request) (*actionReq, bool) {
	if !originAllowed(req, m.cfg) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return nil, false
	}
	mode := modeNative
	if isDatastarRequest(req) {
		mode = modeDatastar
	}
	m.capBody(w, req, mode)
	in, ok := m.decodeSignals(w, req, mode)
	if !ok {
		return nil, false
	}
	return &actionReq{
		w:     w,
		req:   req,
		mode:  mode,
		child: req.PathValue("child"),
		act:   req.PathValue("act"),
		in:    in,
		base:  concreteBase(m.patternBase, req, m.names),
		tab:   actionTab(req, mode, in),
	}, true
}

// refuseEnter answers the registry outcomes that end an action before any unit
// runs, and reports whether it did.
func (m *mount) refuseEnter(w http.ResponseWriter, out enterOutcome) bool {
	switch out {
	case enterGone:
		noteErr(w, ErrStaleTab)
		http.Error(w, "stream closed", http.StatusGone)
	case enterAbandoned:
		http.Error(w, "request abandoned", http.StatusGone)
	case enterClosed:
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
	case enterFull:
		m.warnParkedFull()
		http.Error(w, "too many actions waiting for their stream", http.StatusServiceUnavailable)
	default:
		return false
	}
	return true
}

// routeToStream dispatches over lc when it holds the addressed unit, and
// reports whether it answered; false leaves the action to the plain path.
func (m *mount) routeToStream(ar *actionReq, lc *tabStream, ticket *parkTicket, deadline time.Time) bool {
	if lc.mount != m {
		ticket.leave()
		// The registry is router-wide, and a tab id from another mount is
		// otherwise structurally valid here (same field, same action-id
		// space), so nothing else would fail first.
		http.Error(ar.w, "no such child", http.StatusGone)
		return true
	}
	// A streaming page's plain child echoes the tab id too, and its address
	// was never registered on the connection. Membership is stable (a unit
	// is published before the tab id is, and never removed), so this check
	// is safe off the stream goroutine, unlike the staleness lookup
	// dispatchOverStream still does there.
	if lc.unit(ar.child) == nil {
		ticket.leave()
		return false
	}
	m.dispatchOverStream(ar, lc, deadline, ticket)
	return true
}

// actionTab reads the tab id an action POST echoes, from the form field or the
// signal body per mode.
func actionTab(req *http.Request, mode actionMode, in map[string]json.RawMessage) string {
	if mode == modeNative {
		return req.PostFormValue(tabFormField)
	}
	tab := ""
	if raw, ok := in[tabSignal]; ok {
		// Anything that is not a JSON string leaves tab empty and falls through
		// to the plain path — the same graceful answer a stale id gets.
		_ = json.Unmarshal(raw, &tab)
	}
	return tab
}

func (m *mount) capBody(w http.ResponseWriter, req *http.Request, mode actionMode) {
	max := m.cfg.maxBody
	if mode == modeNative {
		// A native submit is multipart, so the cap rises to the upload cap;
		// file parts past maxBody spill to a temp file the caller removes.
		max = m.cfg.maxUpload
	}
	req.Body = http.MaxBytesReader(unwrapWriter(w), req.Body, max)
}

// decodeSignals decodes an action POST's body per mode. capBody must already
// be installed on req.Body — every caller runs it first.
func (m *mount) decodeSignals(w http.ResponseWriter, req *http.Request, mode actionMode) (map[string]json.RawMessage, bool) {
	if mode == modeNative {
		if err := req.ParseMultipartForm(m.cfg.maxBody); err != nil {
			// multipart's own limits (text fields past maxBody+10 MiB, too
			// many parts) are a size verdict too, not a malformed form.
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok || errors.Is(err, multipart.ErrMessageTooLarge) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return nil, false
			}
			http.Error(w, formError(req, err), http.StatusBadRequest)
			return nil, false
		}
		return nil, true
	}
	in := map[string]json.RawMessage{}
	dec := json.NewDecoder(req.Body)
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return nil, false
		}
		http.Error(w, "malformed request body", http.StatusBadRequest)
		return nil, false
	}
	if in == nil {
		// A JSON null decodes to a nil map, and connect keeps this map as the
		// stream's client slots, which every live action writes into.
		in = map[string]json.RawMessage{}
	}
	return in, true
}

// formError says why a request without Datastar-Request: true was refused.
// The header alone picks the decoder, so a hand-rolled client that forgets it
// gets its JSON parsed as a form; naming the header points at the fix.
func formError(req *http.Request, err error) string {
	if !errors.Is(err, http.ErrNotMultipart) {
		return "malformed form"
	}
	if mt, _, _ := mime.ParseMediaType(req.Header.Get("Content-Type")); mt == "application/json" {
		return "JSON action body without a Datastar-Request: true header; " +
			"without it the POST is read as a native multipart/form-data form submit"
	}
	return "no Datastar-Request: true header, and the body is not multipart/form-data: " +
		"an action is a Datastar @post (JSON signals) or a native form submit"
}

// sentinelFail turns a recovered panic that is one of via's request sentinels
// into its answer, or nil for anything else, which is a handler fault. Both
// transports answer through it, so a sentinel means the same status on a plain
// request and over a stream.
func sentinelFail(log *slog.Logger, rec any) *actionFail {
	switch v := rec.(type) {
	case badActionArg:
		// 400, not 500: the client sent an arg that does not decode.
		return &actionFail{status: http.StatusBadRequest, body: "bad action arg: " + v.err.Error()}
	case unrenderedArg:
		return failGone(v.body(log))
	case paramMiss:
		// The URL names something that does not exist: a 404, not a server
		// fault worth a stack dump.
		return &actionFail{status: http.StatusNotFound, body: "not found", note: ErrNotFound}
	}
	return nil
}

// unknownAction is the 410 body for an id this render does not bind — a branch
// closed, or (the common wiring mistake) an OnInit that failed to restore the
// UI state the View branches on. The bound list goes to the log only: it is a
// map of the render's Go type and method names.
//
// Deduped per unknown id per Router, like warnNoChange: the line dumps the
// whole action table, so an unauthenticated POST loop over garbage ids would
// otherwise print the render's method names at line rate — a log-flood
// amplifier and a disclosure channel in one. The id comes off the URL, so one
// of a shape via never mints is answered without touching the dedupe set, and
// the set itself is capped and reset hourly (see idDedupe).
func (m *mount) unknownAction(u *Ctx, act string) string {
	if !isActionID(act) {
		return "no such action; this render does not bind it"
	}
	switch fresh, filled := m.unknownActs.add(act); {
	case fresh:
		have := make([]string, 0, len(u.actions))
		for id, a := range u.actions {
			have = append(have, id+" ("+a.name+")")
		}
		sort.Strings(have)
		m.cfg.log.Warn("via: no such action; this render binds others", "act", act, "binds", strings.Join(have, ", "))
	case filled:
		m.cfg.log.Warn("via: "+strconv.Itoa(unknownActionCap)+" distinct unknown action ids logged this hour; "+
			"further unknown action ids still answer 410 but are not logged until the hour is up", "act", act)
	}
	return "no such action " + act + "; this render does not bind it"
}

// unknownActionCap bounds idDedupe. A deploy that drops handlers leaves open
// tabs posting a few dozen stale ids at most; anything past this is a client
// making ids up.
const unknownActionCap = 1024

// unknownActionReset is how often idDedupe forgets what it logged, so made-up
// ids that fill the cap cannot silence for good the stale-id warnings a deploy
// that dropped a handler needs.
const unknownActionReset = time.Hour

// idDedupe remembers which request-supplied ids were already logged. The ids
// come from the client, so it stops admitting keys at unknownActionCap until
// the next reset rather than grow under a loop of made-up ones.
type idDedupe struct {
	mu    sync.Mutex
	seen  map[string]struct{}
	full  bool
	since time.Time
}

// add reports whether key is new and admitted, and, separately, whether this
// call is the first one the cap turned away in this interval.
func (d *idDedupe) add(key string) (fresh, filled bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if now := time.Now(); now.Sub(d.since) >= unknownActionReset {
		d.seen, d.full, d.since = nil, false, now
	}
	if _, ok := d.seen[key]; ok {
		return false, false
	}
	if len(d.seen) >= unknownActionCap {
		filled, d.full = !d.full, true
		return false, filled
	}
	if d.seen == nil {
		d.seen = map[string]struct{}{}
	}
	d.seen[key] = struct{}{}
	return true, false
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

// writePage writes the full HTML document ctx rendered — the GET, and the
// full-page re-render a native <form> submit answers with. Liveness is read off
// this render rather than assumed: a plain root may carry a live child, and
// hard-coding "not live" shipped that page with no data-init, leaving the child
// dead after the first form submit. A nil ctx means the render already
// answered.
func (m *mount) writePage(w http.ResponseWriter, req *http.Request, ctx *Ctx, body []byte) {
	if ctx == nil {
		return
	}
	tab := ""
	if len(liveUnits(ctx)) > 0 {
		tab = m.newTabID(pageTabCredential(ctx, m.sessions, req))
	}
	m.writeHTMLPage(w, body, ctx.base, tab, ctx.unitV.v)
}

// renderFreshPage runs OnInit on a fresh instance and renders it for
// writePage. sess non-nil is the session an action left, which the request's
// cookie predates.
func (m *mount) renderFreshPage(w http.ResponseWriter, req *http.Request, base string, sess *Session) (*Ctx, []byte) {
	inst := m.newInst()
	ctx := newRootCtx(true, base)
	ctx.unitV = inst // the root is a unit like any child, when it is live
	if sess != nil {
		// Detached from the action's stream: this page's tab is not that one.
		s := *sess
		s.stream = nil
		ctx.session = &s
	}
	// Before OnInit, not just before the View: seeding an island with Set is
	// what OnInit is for, and a Set with no slot and no pass goes nowhere.
	prebindSignals(ctx, inst)
	if runOnInit(inst.v, ctx, m.scope(w, req), false) != nil {
		return nil, nil
	}
	return ctx, renderRootWith(ctx, inst.v)
}

// connectClient is the part of a connect body a live connection keeps as
// lc.client for its life and walks on every push: values for the non-CS signal
// fields of the units bind rendered, minus those this render binds that do not
// decode.
//
// Fields, not this render's hydrators: a Bind() inside a branch the client's
// own value opens has no hydrator until a push's display render sees that
// value, and its slot must survive until then.
func connectClient(bind *Ctx, in map[string]json.RawMessage) map[string]json.RawMessage {
	keep := map[string]json.RawMessage{}
	keepSignalValues(bind, in, keep)
	hydrateTree(bind, keep, map[string]bool{}) // drops what does not decode
	bind.rev.restore()
	return keep
}

// keepSignalValues copies into keep the values in carries for the non-CS
// signal fields of c and its children.
func keepSignalValues(c *Ctx, in, keep map[string]json.RawMessage) {
	if c.unitV.sig != nil {
		for _, f := range c.unitV.sig.fields {
			if raw, ok := in[f.wire(c.scopePrefix())]; ok && !f.cs {
				keep[f.wire(c.scopePrefix())] = raw
			}
		}
	}
	for _, child := range c.children {
		keepSignalValues(child, in, keep)
	}
}

// pruneToAuthority intersects a live display render's action table with the
// authority render's, per handler and per arg — the same intersection
// dispatchPlain does, for the same reason: a posted signal may widen
// what the client sees and must never widen what it may call.
//
// Walked by child ordinal, since that is the dispatch address. A type mismatch
// at a key (a When around a Child that a hydrated signal shifted) fails the
// whole subtree closed rather than authorizing against a unit the authority
// render never looked at.
//
// The top-level handler and per-arg intersection are load-bearing: they are
// what a live action actually dispatches through. The child recursion below
// them is a backstop whose output no current dispatch path reads —
// dispatchOverStream addresses only a top-level unit, and dispatchPlain
// rebinds its own tree from scratch — because live-unit registration is
// frozen at connect time and a nested child's key can never alias a
// registered unit address. It becomes load-bearing the day live-under-live is
// permitted, or registration ever descends into a pushed bind.
func pruneToAuthority(bind, auth *Ctx) {
	if auth == nil || bind.unitV.typ != auth.unitV.typ {
		clear(bind.actions)
		for _, child := range bind.children {
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
		pruneArgs(a, authAct)
	}
	for i, child := range bind.children {
		var ac *Ctx
		if i < len(auth.children) {
			ac = auth.children[i]
		}
		pruneToAuthority(child, ac)
	}
}

// assertRenderInvariantLiveness fails an action that turned a unit live on a
// page served plain. Only the GET's verdict bootstraps an SSE stream, so a
// State reached through a branch closed at GET leaves the tab demanding a
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
// Datastar v1.0.4 answers a text/javascript response by building a <script>,
// copying the datastar-script-attributes header's JSON onto it as attributes,
// setting its text to the body and appending it to document.head (see the
// client's Content-Type switch, next to its text/html and application/json
// branches). So the TARGET rides in an attribute and THESE BYTES NEVER CHANGE —
// which is what lets the strict CSP admit the script by SHA-256, exactly like
// reconnectInit. Datastar also stamps the document's nonce on it; the hash
// keeps the redirect working without relying on that. Edit this string and
// buildCSP re-derives the hash, but the two must stay byte-identical or the
// browser drops the script and the redirect is silently dead again.
const redirectInit = `(()=>{var s=document.currentScript;if(!s)return;` +
	`var u=s.getAttribute('data-via-to');s.remove();if(u)location.assign(u)})()`

// writeRedirectScript answers a @post with the navigation script. The target is
// JSON-encoded into a header and reaches the DOM through setAttribute, never
// through the HTML parser, and answerRedirect has already cleared it through
// render.SafeURL (see redirectTo) — so no javascript: target and no attribute
// escape.
func writeRedirectScript(w http.ResponseWriter, target string) {
	attrs, err := json.Marshal(map[string]string{"data-via-to": target})
	if err != nil {
		http.Error(w, "redirect failed", http.StatusInternalServerError)
		return
	}
	hdr := w.Header()
	setContentType(hdr, "text/javascript; charset=utf-8")
	hdr.Set("Datastar-Script-Attributes", string(attrs))
	w.Write([]byte(redirectInit))
}

// answerRedirect is dispatch's one redirect policy for every action POST —
// root, child, live, or native form. It writes a queued Redirect and reports
// whether it did: a native submit gets a 303, a Datastar @post gets the
// navigation script above. Either way the target must clear Redirect's host
// check and render.SafeURL, the same URL policy runOnInit and rendered href/src
// URLs use; a refused one is logged and dropped, not followed, and the caller
// answers as if none was queued.
func (m *mount) answerRedirect(w http.ResponseWriter, req *http.Request, mode actionMode, redirect redirectTo) bool {
	if redirect.url == "" {
		return false
	}
	switch refused := redirect.refusal(); {
	case refused != "":
		m.cfg.log.Warn("via: Redirect target dropped", "redirect", redirect, "reason", refused)
		return false
	case mode == modeNative:
		http.Redirect(w, req, m.fromPage(req, redirect.url), http.StatusSeeOther)
	default:
		writeRedirectScript(w, redirect.url)
	}
	return true
}

// fromPage resolves a relative target against the page the action was
// submitted from, as the browser does for the @post script. http.Redirect
// would resolve it against the action URL.
func (m *mount) fromPage(req *http.Request, target string) string {
	u, err := url.Parse(target)
	if err != nil || u.IsAbs() || strings.HasPrefix(u.Path, "/") {
		return target
	}
	page, err := url.Parse(concreteBase(m.patternBase, req, m.names) + m.pageSuffix)
	if err != nil {
		return target
	}
	return page.ResolveReference(u).String()
}

// writePatch answers a Datastar action with its element-patch fragment, or 204
// for a nil one: unchanged, or already carried by the live push. Fragments take
// the default policy: a fragment carries no document, so its header is inert —
// only the page's policy governs what runs.
func writePatch(w http.ResponseWriter, b []byte) {
	if b == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	hdr := w.Header()
	setContentType(hdr, "text/html; charset=utf-8")
	hdr.Set("Content-Security-Policy", cspHeader)
	w.Write(b)
}
