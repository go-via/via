package via

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
)

// liveUnits collects every live unit in bind's render, at any depth — the root
// first when live, then its live children in render order.
func liveUnits(bind *Ctx) []*Ctx {
	var units []*Ctx
	if bind.live {
		units = append(units, bind)
	}
	appendLiveChildren(bind, &units)
	return units
}

// appendLiveChildren recurses: a live grandchild gets no stream wiring unless
// discovery descends past its (live or plain) parent too.
func appendLiveChildren(ctx *Ctx, out *[]*Ctx) {
	for _, isl := range ctx.children {
		if isl.live {
			*out = append(*out, isl)
		}
		appendLiveChildren(isl, out)
	}
}

// connectUnit wires unit's push to stream (whole-page at #root for the root,
// its own #via-i{key} container for a child) and registers it on lc. The
// returned pusher's baseline renders the unit as the push would and records the
// bytes without framing them, so a later push ships only what actually changed.
func connectUnit(unit *Ctx, stream *stream, base string, lc *tabStream) *unitPusher {
	lc.replace(unit)
	unit.guard = lc.guard
	p := &unitPusher{lc: lc, stream: stream, inst: unit.unitV, base: base, from: unit, last: unit}
	if unit.isChild {
		p.childKey = unit.childKey
		p.frameID = "via-i" + unit.childKey
	}
	unit.pusher = p
	return p
}

// livePush renders one live unit under the same two-phase rule dispatchPlain
// has always used (I1/I2) and the live path had no version of at all: the
// authority render is the one the client's posted signals did not touch, and it
// alone decides what is dispatchable.
//
// Phase 1 puts the server-authored signal values back — undoing the previous
// cycle's application of lc.client, which otherwise became the server's own
// state, because hydration writes straight into a field of an instance that
// outlives the request — and renders. Phase 2 applies the client's signals to
// the slots that render made writable and re-renders to fixpoint, exactly as
// the plain path does, so a Bind()ed signal still steers what the client sees
// and a branch it opens still gets the slots inside it hydrated. The frame
// carries phase 2; the table the connection keeps is phase 2 pruned to phase 1,
// per handler and per arg.
//
// The authority render runs first and the display render last because
// Signal.bind stamps the dirty sink at render time: the Ctx registered on the
// connection must be the one a later Set writes through.
//
// Cost: two renders per push on any page carrying a Bind() — a real Datastar
// connect body is the whole signal store, so lc.client holds every bindable
// slot from the first connect on. Each extra render re-runs every plain child's
// OnInit (inheritRequestScope sets doInit) and re-walks checkLiveNesting.
func livePush(lc *tabStream, p *unitPusher) (*Ctx, []byte) {
	// One set for the life of the connection, never replaced: a hydrator closure
	// notes its undo into the rev of the Ctx that bound the signal, so a second
	// live unit's push swapping lc.rev would leave that unit pointing at a set
	// nothing restores — and its next action's client value would survive into
	// the authority render. restore() clears the map, so reuse is not growth:
	// it is keyed by slot and bounded by the page's signal count.
	rev := lc.rev
	rev.restore()
	// Deferred, not called at the end: the display render below runs with the
	// client's values hydrated onto the instance, and a panic in it (which
	// runPushItem swallows, leaving the stream up) would otherwise leave
	// attacker-posted values sitting on the instance for the next Tick/Listen
	// handler to read. body is returned before the defer fires, so the frame
	// the client sees is unaffected. The leading restore stays: it covers the
	// window before this defer is registered, and the initOutcome recover path.
	defer rev.restore()
	auth, body := p.render(rev)
	bind := auth
	done := map[string]bool{}
	n := 0
	for ; n < maxHydratePasses && hydrateTree(bind, lc.client, done); n++ {
		bind, body = p.render(rev)
	}
	if n == maxHydratePasses {
		lc.mount.cfg.log.Warn("via: live push hit its hydration-pass cap; some posted signals may be unapplied",
			"passes", maxHydratePasses)
	}
	if bind != auth {
		pruneToAuthority(bind, auth)
	}
	return bind, body
}

// unitPusher re-renders one live unit and frames it on the stream: the whole
// page at #root for the root, its #via-i{key} container for a child. The fresh
// bind Ctx replaces lc's entry, so a live action needs no render of its own —
// the previous push already built its actions/hydrators table.
type unitPusher struct {
	lc     *tabStream
	stream *stream
	inst   instance
	base   string
	// from is what makes a live root's plain children survive a push: Child
	// re-copies each child from the root's field every render, so a child whose
	// fields OnInit filled comes back zero-valued unless that OnInit runs again.
	// Cost: a plain child of a live root runs its OnInit once per pushed frame —
	// keep it cheap, or hold the data on the live root. A live child may not
	// Child at all (checkLiveNesting), so a child's push ignores it.
	from        *Ctx
	last        *Ctx   // the bind a Tick/Listen/action handler's Sets landed on; the connect render's until the first push
	childKey    string // empty for the root
	frameID     string // the child's container id; empty for the root
	lastBody    []byte // nil until the first frame, so the first push always ships
	fatalLogged bool
}

func (p *unitPusher) render(rev *revertSet) (*Ctx, []byte) {
	if p.frameID == "" {
		return renderRootBase(p.inst, false, p.base, nil, nil, p.from, rev, p.lc.badDecodeLogged) // push omits data-signals
	}
	// false: this unit's own ancestor chain is plain by construction — a live
	// unit could never have connected otherwise (checkLiveNesting already proved
	// it at connect). Its own children are under a live ancestor regardless,
	// since checkLiveUnderLive folds in c.live itself.
	return renderChildBind(p.childKey, p.inst, p.base, nil, rev, p.lc.badDecodeLogged, false)
}

// push renders and frames the unit. Unchanged frames are dropped (the
// canonical statement; the tests refer here as "skipUnchanged"). A Tick that
// changes nothing rendered would otherwise ship a byte-identical element-patch
// every beat, which is the CPU and bandwidth floor of an idle connection at
// scale (an idle page on a 2s tick sent ~160 identical frames in five
// minutes). The plain action path has always answered 204 on an unchanged
// render; this is the live path's version of that.
//
// It is element patches only. flushDirty runs first and on its own comparison
// (the dirty set), so a signal change with an unchanged DOM still ships its
// patch-signals frame, and a DOM change with no signal change still ships its
// element patch. The connect handshake's signals frame and the keepalive are
// likewise untouched, so a half-open peer is still detected on the beat.
func (p *unitPusher) push() {
	p.lc.flushDirty(p.last)
	defer p.recoverFatal()
	bind, body := livePush(p.lc, p)
	bind.pusher = p
	p.last = bind
	p.lc.replace(bind)
	if bytes.Equal(body, p.lastBody) {
		return
	}
	p.lastBody = append(p.lastBody[:0], body...)
	p.stream.patch(p.frameID, body)
}

// baseline records the unit's current render as the last framed body without
// framing it.
func (p *unitPusher) baseline() {
	_, body := livePush(p.lc, p)
	p.lastBody = append(p.lastBody[:0], body...)
}

// recoverFatal tears the stream down on a panic that repeats on every frame,
// logging it once; any other panic propagates to runPushItem's per-item
// recover, which logs it and keeps the stream up.
//
// For the root it is a plain child's failed OnInit: initOutcome panics from
// inside the render on every frame, and a push has no response to turn that
// into a 500/303/404. Dropping the frame loops forever with no client-visible
// signal at all, and rendering the child empty serves a page that is quietly
// wrong. For a child it is a live grandchild grown under it: the tree shape,
// not the data, is what's wrong. Either way the client's reconnect re-requests
// the page and gets the real answer on a path that can give one.
func (p *unitPusher) recoverFatal() {
	rec := recover()
	if rec == nil {
		return
	}
	var msg string
	var attrs []any
	switch v := rec.(type) {
	case initOutcome:
		if p.frameID != "" {
			panic(rec)
		}
		msg = "via: live push aborted — an embedded child's OnInit failed; tearing the " +
			"stream down so the client reconnects and gets the real answer"
		attrs = []any{"err", v.err, "redirect", v.redirect}
	case liveNestingViolation:
		if p.frameID == "" {
			panic(rec)
		}
		msg = "via: live push aborted — this child grew a live descendant of its " +
			"own, which via.Child cannot host; tearing the stream down so the client reconnects and " +
			"gets the real answer"
		attrs = []any{"err", v.msg}
	default:
		panic(rec)
	}
	if !p.fatalLogged {
		p.fatalLogged = true
		p.lc.mount.cfg.log.Error(msg, attrs...)
	}
	p.stream.abort()
}

// connect is the SSE stream's entry point at {base}/_via/sse. Every mount gets
// the route; a page with no live content answers 404 on it.
func (m *mount) connect(w http.ResponseWriter, req *http.Request) {
	// Origin floor first: the stream opens a long-lived goroutine + timers, so
	// reject an unprovable source before allocating any of it.
	if !originAllowed(req, m.cfg) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	// A POST so the connect can carry the page's signals as a body.
	m.capBody(w, req, modeDatastar)
	connectSig, ok := m.decodeSignals(w, req, modeDatastar)
	if !ok {
		return
	}
	if !canFlush(w) {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	base := concreteBase(m.patternBase, req, m.names)
	// Increment-then-check so the gauge can't be raced past the limit.
	if n := m.liveCount.Add(1); n > int64(m.maxLive) {
		m.liveCount.Add(-1)
		m.warnAtCapacity(n - 1)
		http.Error(w, "stream capacity reached", http.StatusServiceUnavailable)
		return
	}
	defer m.liveCount.Add(-1)
	// A connect arriving after Router.Close is refused rather than opened and
	// torn down a microsecond later, so a client reconnecting into a shutdown
	// gets one deterministic answer. The AfterFunc below is what makes the race
	// with a Close landing just after this check harmless: it fires immediately
	// on an already-cancelled router context.
	if m.routerCtx != nil {
		// Under liveMu so the check and the Add are one step against Close: an
		// Add landing while Close is already parked in Wait is a WaitGroup
		// misuse throw, not a recoverable panic.
		m.liveMu.RLock()
		select {
		case <-m.routerCtx.Done():
			m.liveMu.RUnlock()
			http.Error(w, "server shutting down", http.StatusServiceUnavailable)
			return
		default:
		}
		m.live.Add(1)
		m.liveMu.RUnlock()
		defer m.live.Done()
	}
	headersSent := false
	defer func() {
		if rec := recover(); rec != nil {
			// A panic before the headers went out would otherwise fall through
			// to Go's default — 200 with an empty body, telling the client the
			// connect succeeded. Once headers are sent this can't help, so a
			// mid-stream panic is caught per push item (runPushItem) instead.
			if !headersSent {
				recoverToHTTP(m.cfg.log, w, req, rec, "stream", true)
			}
		}
	}()
	pv := m.newInst()
	// A half-open peer never cancels req.Context(); a failed frame write is the
	// only signal it's gone, so the stream needs a context it can cancel.
	streamCtx, cancel := context.WithCancel(req.Context())
	defer cancel()
	// Router.Close reaches the stream through here rather than through the
	// context chain: req.Context() is net/http's, and http.Server.Shutdown does
	// not cancel it, so nothing else would ever end this goroutine, its Tick
	// timers or its Listen subscriptions.
	stream := &stream{
		w:       w,
		rc:      http.NewResponseController(w),
		timeout: sseWriteTimeout,
		cancel:  cancel,
	}
	if m.routerCtx != nil {
		defer context.AfterFunc(m.routerCtx, stream.end)()
	}

	// The credential dispatch requires a match against, so that a leaked tab id
	// is not on its own a bearer token good from any origin with no session at
	// all (see dispatch). Resolved before OnInit, which may mint or rotate one:
	// the point is the identity the browser held when it opened this stream.
	// An error here is not "anonymous": the browser may well hold a valid
	// cookie the store just could not answer for. Opening the stream anyway
	// would leave it permanently unbound — nothing binds it later, since a
	// live action sees a session that already existed at connect — and the tab
	// id would be a bearer credential for the connection's whole life. Fail the
	// connect instead; the client's reconnect logic retries.
	sessID, sessDat, err := m.sessions.resolve(req)
	if err != nil {
		http.Error(w, "session store unavailable", http.StatusServiceUnavailable)
		return
	}

	// The page's own id when this app issued it to this credential and no
	// other stream holds it; a fresh one otherwise, sent in the first frame
	// either way. Claimed before OnInit so an action already parked on the id
	// waits for this stream instead of timing out behind it.
	id := m.claimTab(connectSig, sessID)
	registered := false
	defer func() {
		if !registered {
			m.reg.del(id, m.tombFor(stream))
		}
	}()

	// OnInit runs on the very Ctx the discovery render then binds, so a
	// Tick/Listen it registers is what makes the root a live unit.
	//
	// Un-hydrated, unlike every earlier version of this line: the connect body
	// is the client's, and this render is what decides the connection's action
	// table and its liveness (I1). The body is kept as lc.client and applied to
	// the display render of every push instead (livePush) — which is the only
	// place it was ever visible, since connect frames no elements of its own.
	rev := newRevertSet()
	logFlag := new(atomic.Bool) // dedupes the decode-failure Warn for this connection's whole life
	bind := newRootCtx(false, base, nil)
	bind.rev = rev
	bind.badDecodeLogged = logFlag
	bind.unitV = pv
	prebindSignals(bind, pv)
	// OnInit gets the load above rather than a second one: when that load slid
	// the idle window, only its handle knows to re-send the cookie, and a
	// reconnect may be the only request an open tab ever makes.
	bind.req, bind.sessions, bind.policy, bind.sessW = req, m.sessions, m.policy, w
	bind.adoptSession(sessID, sessDat, nil)
	if runOnInit(pv.v, bind, w, req, m.sessions, m.policy, true) != nil {
		return
	}
	renderRootWith(bind, pv.v)
	units := liveUnits(bind)

	if len(units) == 0 {
		http.Error(w, "no stream for this tab", http.StatusNotFound)
		return
	}

	guard := newPushGuard(fmt.Sprintf(" [tab=%s unit=%T]", id, pv.v))

	var connSID string
	if sessDat != nil {
		connSID = sessDat.sid
	}

	// Built before the connect loop so each pusher can register its unit as
	// the current unit on every render — a live action always runs against the
	// last render's actions/hydrators.
	lc := &tabStream{
		id:              id,
		mount:           m,
		pushq:           make(chan pushItem),
		done:            streamCtx.Done(),
		units:           map[string]*Ctx{},
		sess:            connSID,
		handlers:        units,
		client:          connectClient(bind, connectSig),
		rev:             rev,
		badDecodeLogged: logFlag,
		end:             stream.end,
		wire:            stream,
		guard:           guard,
	}

	// runStream owns the disposer sweep but is not running yet: an OnConnect fn
	// that panics before it takes over would strand every acquire already made
	// (a room joined with no matching part, for the life of the process). Armed
	// here and handed over at the call, and deferred after the recover above so
	// it runs first.
	streaming := false
	defer func() {
		if streaming {
			return
		}
		for _, u := range units {
			for _, d := range u.disposers {
				runRecovered(m.cfg.log, guard, d)
			}
		}
	}()

	// Subscriptions are started before any OnConnect runs, and their queued
	// values are drained by runStream below. A unit whose OnConnect publishes
	// (the "join a room" pattern the hook's doc names) would otherwise miss its
	// own join: subscriptions used to start inside runStream, i.e. after every
	// OnConnect, so the join was published into a topic this unit had not
	// subscribed to yet and the first frame showed the pre-join world. The
	// subscriptions stay off the plain-GET path — this is the SSE handler, and
	// Ctx.Listen only records one at OnInit time.
	//
	// One wake channel shared by every subscription: a Listen used to cost a
	// goroutine per subscription per connection purely to bridge its Ready
	// channel onto runStream's select. Capacity 1 and coalescing, so a
	// publisher never blocks and N pending values still cost one sweep.
	wake := make(chan struct{}, 1)
	var listeners []subscription
	for _, u := range units {
		u.streamCtx = streamCtx
		for _, l := range u.subs {
			l.start(wake)
			listeners = append(listeners, l)
		}
	}

	for _, u := range units {
		p := connectUnit(u, stream, base, lc)
		// The post-connect push below compares against the unit's last framed
		// body; without this it would be empty and that push would ship a full
		// frame even for an OnConnect fn that changed nothing.
		if len(u.onConnect) > 0 {
			p.baseline()
		}
		for _, fn := range u.onConnect {
			fn()
		}
		// OnInit may have minted a session where the connect cookie left
		// lc.sess empty — bind it now so the connection isn't left as a
		// bare-tab-id credential.
		lc.bindSession(u.session.sid())
	}

	// Watched before the first sweep and the OnConnect pushes, which run app
	// handlers: a Rotate elsewhere while they run must end this stream. Not
	// before the OnConnect fns, which may mint the session; a Rotate during
	// one waits for the first beat's revalidate.
	lc.watchSession(bind.session)
	defer lc.unwatchSession()

	m.reg.put(id, lc) // a live action POST routes to this connection by tab id
	registered = true
	defer func() { m.reg.del(id, m.tombFor(stream)) }()

	writeSSEHeaders(w)
	w.WriteHeader(http.StatusOK)
	headersSent = true
	stream.signals(`{"` + tabSignal + `":"` + id + `"}`)

	// The connect response is flushed now. A Tick/Listen handler runs against
	// this same unit Ctx for the life of the connection, so leaving sessW set
	// would let its Session().Put SetCookie on a dead response: no error, no
	// cookie, a session minted and orphaned until TTL (I2). Clearing it makes
	// Session.ensure's "no cookie can be set" warning fire, as documented.
	for _, u := range units {
		u.sessW = nil
		if u.session != nil {
			u.session.w = nil // the handle cached w at resolve time; clearing the Ctx alone left it live
		}
	}

	// Whatever an OnConnect published is drained first, so the connect's own
	// frame carries it rather than a pre-handler render the next sweep would
	// immediately correct.
	guard.watchPin(m.cfg.pinnedDeadline, func() { lc.warnPinned(&lc.pinTimerLogged) })
	sweepListeners(m.cfg.log, guard, listeners)

	// A Set inside an OnConnect fn has no push to ride — runStream does no
	// initial push — so it must be pushed explicitly here. It ships a frame
	// only if the render moved off the baseline taken before the fns ran.
	for _, u := range units {
		if len(u.onConnect) > 0 {
			runPushItem(m.cfg.log, guard, pushItem{push: u.pusher})
		}
	}

	streaming = true
	runStream(m.cfg.log, streamCtx, guard, units, listeners, wake, lc, sseHeartbeat)
}
