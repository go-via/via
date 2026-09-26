package via

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// tabStream is a connected tab's live units, kept in the registry so a POST
// action routes onto its single goroutine. units is replaced after every push
// with that render's bind Ctx — always the render the client's DOM reflects.
type tabStream struct {
	mount       *mount            // a tab id is valid only on its mount, never another sharing the router-wide registry
	pushq       chan func()       // serialization channel, shared by all this connection's units
	done        <-chan struct{}   // closed on disconnect
	pushSignals func(json string) // emit a patch-signals frame on this stream
	mu          sync.Mutex        // replace runs on the child goroutine, unit is read from the dispatching request's
	units       map[string]*Ctx   // dispatch address → current unit Ctx, at any embedding depth
	sess        string            // the bound session's stable sid; "" when anonymous. An sid, not the cookie id, so a Rotate (which re-ids) and another pod both still match
	handlers    []*Ctx            // the connect-time unit Ctxs, which every Tick and Listen handler is handed for the connection's life

	// client and rev are touched only on this connection's own goroutine —
	// connect builds them before runStream, and every push and every live
	// action reaches them through pushq — so neither takes mu.
	client          map[string]json.RawMessage // the slots the client last posted, re-applied to every display render (livePush)
	rev             *revertSet                 // how to undo that application before the next authority render
	badDecodeLogged *atomic.Bool               // dedupes the hydrator's decode-failure warning for this connection's life

	id           string      // the per-connection tab id, for correlating a log line with a tab
	pinnedLogged atomic.Bool // warnPinned is once per connection, not once per click
	// pinTimerLogged is the stream timer's own once-flag: a Tick that ran long
	// once must not silence the action that later finds the tab truly pinned.
	pinTimerLogged atomic.Bool

	end      context.CancelFunc // closes the stream cleanly, from any goroutine
	wire     *stream            // for what the goroutine is blocked on, when it is pinned
	guard    *pushGuard         // handed to each unit, so a Listen handler's recover dedupes with the rest
	sessID   string             // under mu: the cookie id the handlers' Session holds
	sessData *sessionData       // under mu: the copy the handlers read, which sessionManager.fanout refreshes
}

// watchSession indexes this connection under s, so a write or Rotate through
// another request reaches its handlers (sessionManager.fanout, rotated).
func (c *tabStream) watchSession(s *Session) {
	sid := s.sid()
	if sid == "" {
		return
	}
	c.mu.Lock()
	c.sessID, c.sessData = s.id, s.data
	c.mu.Unlock()
	c.mount.sessions.watch(sid, c)
}

func (c *tabStream) unwatchSession() {
	if d := c.watchedData(); d != nil {
		c.mount.sessions.unwatch(d.sid, c)
	}
}

func (c *tabStream) watched() (string, *sessionData) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessID, c.sessData
}

func (c *tabStream) watchedData() *sessionData {
	_, d := c.watched()
	return d
}

// moveSession follows this tab's own Rotate. Only its own goroutine calls it,
// the one every handler Ctx is touched from.
func (c *tabStream) moveSession(oldID, newID string) {
	c.mu.Lock()
	if c.sessID == oldID {
		c.sessID = newID
	}
	c.mu.Unlock()
	for _, u := range c.handlers {
		if u.session != nil && u.session.id == oldID {
			u.session.id = newID
		}
	}
}

// revalidate runs on the keepalive beat. The in-process index cannot hear a
// Rotate, a logout or an expiry on another process sharing the store, so the
// stream reads its session back and closes if it is gone; the reconnect then
// resolves whatever the cookie names now. A store error is an outage, not a
// revocation: the stream stays up and the next beat asks again.
func (c *tabStream) revalidate() {
	select {
	case <-c.done:
		return
	default:
	}
	id, d := c.watched()
	if d == nil {
		return
	}
	c.wire.busy.Store(busyStore)
	vals, exp, live, err := c.mount.sessions.peek(id, d.sid)
	c.wire.busy.Store(busyNone)
	switch {
	case err != nil:
	case !live:
		c.end()
	default:
		d.refresh(vals, exp)
	}
}

// boundSession is guarded because a live action can bind it after connect,
// racing a concurrent dispatch's read on its own goroutine.
func (c *tabStream) boundSession() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess
}

// bindSession records sid as this connection's credential on first mint: the
// connect cookie, or a session a live action established after connect.
// Idempotent: a later Rotate is a no-op here, since the sid survives it.
func (c *tabStream) bindSession(sid string) {
	if sid == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess == "" {
		c.sess = sid
	}
}

// shareSession hands s to the handler Ctxs of a connection that opened with no
// session, once an action has bound the tab to it. A stream that connected
// before its session existed otherwise kept an anonymous Session in every
// Tick and Listen handler for its whole life, even after its own action
// carried the cookie, so per-session addressing never reached it. Only the
// bound session is shared: it is the one every later action must carry.
// Runs on the connection's goroutine, like every handler that reads it.
func (c *tabStream) shareSession(s *Session) {
	sid := s.sid()
	if sid == "" || sid != c.boundSession() {
		return
	}
	for _, u := range c.handlers {
		if u.session.sid() == "" {
			u.adoptSession(s.id, s.data, nil)
		}
	}
	if c.watchedData() == nil {
		c.watchSession(s)
	}
}

func (s *Session) sid() string {
	if s == nil || s.data == nil {
		return ""
	}
	return s.data.sid
}

// flushDirty ships the signals u's handlers wrote since its last push and
// stops the client's stale copies from coming back. Every server-driven change
// on a live connection goes through it — a live action's, and a Tick/Listen
// handler's, which used to Set into nothing: the display render in livePush
// re-hydrates lc.client over the instance, so without the delete the client's
// old value would be painted back over the Set on the very next frame.
//
// It runs before the push's renders so the patch-signals frame precedes the
// element patch, and so the display render no longer sees the superseded slots.
func (c *tabStream) flushDirty(u *Ctx) {
	if u == nil {
		return
	}
	all := u.dirtyAll()
	if len(all) == 0 {
		return
	}
	// A slot the server wrote is no longer the client's to restate.
	for slot := range all {
		delete(c.client, slot)
	}
	// A server-driven signal change reaches the client as its own signal-patch:
	// the element push omits data-signals, so a morph never clobbers what the
	// user is typing.
	if raw, err := json.Marshal(all); err == nil {
		c.pushSignals(string(raw))
	}
	clearDirty(u)
}

func clearDirty(c *Ctx) {
	clear(c.dirty)
	for _, isl := range c.children {
		clearDirty(isl)
	}
}

// replace registers u as the current bind for its dispatch address, so the
// next action targets this render's actions/hydrators table.
func (c *tabStream) replace(u *Ctx) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.units[unitAddr(u)] = u
}

// runOutcome says why a dispatch did not produce a result. One 410 for three
// unrelated causes was the whole defect: a closed tab, a client that hung up,
// and a child goroutine pinned by a blocking Tick/Listen/action handler are
// three different operational problems and only the first is the client's to
// fix.
type runOutcome int

const (
	runOK runOutcome = iota
	runClosed
	runAbandoned
	runPinned
)

// pinnedDeadline is how long a dispatch waits for the child goroutine to reach
// it before declaring the goroutine pinned (see WithPinnedDeadline). Well past
// any sane handler, and short enough to answer before a load balancer or client
// deadline does — the old code waited on req.Context() alone, so the pinned
// case was invisible and arrived as a 410 that blamed the client.
func (c *tabStream) pinnedDeadline() time.Duration { return c.mount.cfg.pinnedDeadline }

// run posts fn onto the child goroutine and waits for its actionResult, so a
// live action's Redirect, session cookie and panic all resolve on the POST that
// triggered it. The wait to be picked up is guarded on c.done, reqCtx and
// deadline so a POST racing a closed tab, one whose client hung up, and one
// whose goroutine never arrives are told apart rather than collapsed.
// deadline is the one the action may already have spent parked in
// registry.enter, so the two waits together never pass the pinned deadline.
// picked runs as soon as the pickup is decided, handing the tab's turn to the
// next parked action.
//
// res.pushWork runs after result is sent, still on this goroutine: the POST
// proceeds at once while pushWork stays serialized in the order its mutation
// ran. A detached goroutine would race other actions' and push out of order.
func (c *tabStream) run(reqCtx context.Context, deadline time.Time, picked func(), fn func() actionResult) (actionResult, runOutcome) {
	result := make(chan actionResult, 1)
	pinned := time.NewTimer(time.Until(deadline))
	defer pinned.Stop()
	outcome := runOK
	select {
	case c.pushq <- func() {
		var res actionResult
		defer func() {
			if rec := recover(); rec != nil {
				c.mount.cfg.log.Error("via: live action panic",
					"tab", c.id, "unit", c.unitType(), "err", rec, "stack", string(debug.Stack()))
				res = actionResult{panicked: true}
			}
			result <- res
			if res.pushWork != nil {
				res.pushWork()
			}
		}()
		res = fn()
	}:
	case <-c.done:
		outcome = runClosed
	case <-reqCtx.Done():
		outcome = runAbandoned
	case <-pinned.C:
		c.warnPinned(&c.pinnedLogged)
		outcome = runPinned
	}
	picked()
	if outcome != runOK {
		return actionResult{}, outcome
	}
	// pushq is unbuffered, so the send above means the stream goroutine is
	// already running fn, and neither a closing stream nor a hung-up client
	// stops it. fn writes this request's w (a session cookie, a redirect), so
	// returning before it finishes would hand w back to net/http mid-write.
	return <-result, runOK
}

// unitType names the Go type driving this connection, for a log line that has
// to point at the handler to go read.
func (c *tabStream) unitType() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if u := c.units[rootAddr]; u != nil && u.unitV.typ != nil {
		return u.unitV.typ.String()
	}
	return "unknown"
}

// warnPinned fires once per connection per path (once is that path's flag): a
// pinned goroutine stays pinned, and every later click would repeat the line.
// Keepalives stop too (runStream's beat is on the same goroutine), so a proxy
// tears the connection down and it leaks for the life of the process; this
// line is the only signal. It fires from an action that could not be picked
// up, and from the stream when one item outlives the deadline, so a tab no one
// clicks on is reported too.
func (c *tabStream) warnPinned(once *atomic.Bool) {
	if once.Swap(true) {
		return
	}
	log, attrs := c.mount.cfg.log, []any{"deadline", c.pinnedDeadline(), "tab", c.id, "unit", c.unitType()}
	switch c.wire.busy.Load() {
	case busyWrite:
		log.Warn("via: tab pinned — its stream goroutine is blocked writing a frame to a client that is not "+
			"reading, so every action on this tab answers 503 until the write deadline ends the stream.",
			append(attrs, "writeTimeout", sseWriteTimeout)...)
	case busyStore:
		log.Warn("via: tab pinned — its stream goroutine is waiting on the session store for the keepalive's "+
			"session check, so every action on this tab answers 503 until it answers (see "+
			"WithSessionStoreTimeout).", attrs...)
	default:
		log.Warn("via: tab pinned — its stream goroutine is inside a Tick, Listen, OnConnect or action "+
			"handler, so its keepalives have stopped too and every action on this tab answers 503 until it "+
			"returns. Move blocking work off the handler.", attrs...)
	}
}

// registry maps a per-connection tab id to its live child. A local of each
// Handler call, never global.
//
// A slot exists while a stream holds its id, an action is parked on it, or
// the server ended its stream less than a pinned deadline ago, so its size is
// bounded by open streams, parked actions (maxParked) and tombstones
// (maxTombstones). A GET that never connects leaves nothing here.
type registry struct {
	key       [32]byte // MACs the tab ids this process issues; see newTabID
	maxParked int
	parkWarn  atomic.Int64 // unix nanos of the last at-capacity log (see mount.warnParkedFull)
	mu        sync.Mutex
	m         map[string]*tabSlot
	parked    int
	// Every tombstone lives the same pinned deadline, so arrival order is
	// expiry order and the oldest is always at the front.
	tombs []tombstone
}

type tabSlot struct {
	lc      *tabStream // set once the holding stream is ready for actions
	claimed bool       // a stream holds this id, ready or still connecting
	// gone is when the tombstone a server-ended stream left expires; until
	// then every action on the id answers 410 at once.
	gone time.Time
	// queue is the parked actions in arrival order. Only the head may hand
	// off to lc, and it leaves the queue once lc has taken it, so a later
	// arrival never overtakes an earlier one.
	queue []*waiter
}

type waiter struct {
	wake chan struct{} // closed when the waiter should look at its slot again; nil once closed
}

func (w *waiter) signal() {
	if w.wake != nil {
		close(w.wake)
		w.wake = nil
	}
}

type tombstone struct {
	id    string
	until time.Time
}

// parkedCap bounds the router's parked actions whatever WithMaxSSEConn says:
// each holds its decoded body, so the worst case is parkedCap × WithMaxBody.
const parkedCap = 1024

// maxTombstones bounds the ids remembered as server-ended. Past it the oldest
// is forgotten early, and an action on it waits out the deadline as before.
const maxTombstones = 4096

func newRegistry(maxLive int) *registry {
	// One parked action per stream slot: a parked action is waiting for a
	// stream to open, and no more than maxLive can.
	r := &registry{m: make(map[string]*tabSlot), maxParked: min(maxLive, parkedCap)}
	if _, err := rand.Read(r.key[:]); err != nil {
		panic("via: crypto/rand failed: " + err.Error())
	}
	return r
}

func (r *registry) slot(id string) *tabSlot {
	s := r.m[id]
	if s == nil {
		s = &tabSlot{}
		r.m[id] = s
	}
	return s
}

func (r *registry) dropIfIdle(id string, s *tabSlot) {
	if s.lc == nil && !s.claimed && len(s.queue) == 0 && s.gone.IsZero() && r.m[id] == s {
		delete(r.m, id)
	}
}

func (r *registry) expireTombs(now time.Time) {
	for len(r.tombs) > 0 && (!now.Before(r.tombs[0].until) || len(r.tombs) > maxTombstones) {
		t := r.tombs[0]
		r.tombs = r.tombs[1:]
		if s := r.m[t.id]; s != nil && s.gone.Equal(t.until) {
			s.gone = time.Time{}
			r.dropIfIdle(t.id, s)
		}
	}
}

func (s *tabSlot) signalHead() {
	if s.lc != nil && len(s.queue) > 0 {
		s.queue[0].signal()
	}
}

// claim reserves id for one stream. It fails when another stream holds it,
// which is what a duplicated browser tab restoring the same document looks
// like: two streams must never answer to one id. It clears a tombstone: the
// id is live again.
func (r *registry) claim(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireTombs(time.Now())
	s := r.slot(id)
	if s.claimed {
		return false
	}
	s.claimed = true
	s.gone = time.Time{}
	return true
}

func (r *registry) put(id string, c *tabStream) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.slot(id)
	s.lc = c
	s.signalHead()
}

func (r *registry) each(fn func(*tabStream)) {
	r.mu.Lock()
	all := make([]*tabStream, 0, len(r.m))
	for _, s := range r.m {
		if s.lc != nil {
			all = append(all, s.lc)
		}
	}
	r.mu.Unlock()
	for _, c := range all {
		fn(c)
	}
}

// del releases id. After a client hang-up or a failed write (tomb 0) parked
// actions keep the slot and go on waiting: the retry reconnects under the
// same id. When the server ended the stream itself it is not coming back
// under this id, so the slot is tombstoned for tomb and its waiters answer
// 410 now.
func (r *registry) del(id string, tomb time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.m[id]
	if s == nil {
		return
	}
	s.lc, s.claimed = nil, false
	if tomb > 0 {
		now := time.Now()
		s.gone = now.Add(tomb)
		r.tombs = append(r.tombs, tombstone{id: id, until: s.gone})
		r.expireTombs(now)
		for _, w := range s.queue {
			w.signal()
		}
	}
	r.dropIfIdle(id, s)
}

type enterOutcome int

const (
	enterReady     enterOutcome = iota // lc is the stream; call leave once it has taken the action
	enterNoStream                      // nothing holds the id and the caller may not park
	enterGone                          // the server ended the id's stream: 410
	enterTimeout                       // no stream took the action by the deadline
	enterAbandoned                     // the client hung up while parked
	enterClosed                        // the router closed while parked: 503
	enterFull                          // the router already holds maxParked parked actions: 503
)

// connectWait caps how long a parked action waits for its tab's stream to
// register. A first connect lands well under it on a healthy network, and a
// click made in a longer gap is better answered by a 410 and a fresh page than
// by holding the POST for the whole pinned deadline.
const connectWait = 2 * time.Second

// enter finds id's stream for one action. A ready stream with no one parked
// ahead is returned at once. Otherwise, when park is set, the action joins
// the slot's queue and waits for its turn at a ready stream: until deadline,
// and for the stream to register at all, until connectWait at most.
// leave must be called once the stream has taken the action (or refused it),
// so the next in line gets its turn.
func (r *registry) enter(ctx, routerCtx context.Context, id string, deadline time.Time, park bool) (lc *tabStream, leave func(), out enterOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.expireTombs(now)
	s := r.m[id]
	switch {
	case s != nil && !s.gone.IsZero():
		return nil, nil, enterGone
	case s != nil && s.lc != nil && len(s.queue) == 0:
		return s.lc, func() {}, enterReady
	case !park:
		return nil, nil, enterNoStream
	case r.parked >= r.maxParked:
		return nil, nil, enterFull
	}
	s = r.slot(id)
	w := &waiter{}
	s.queue = append(s.queue, w)
	r.parked++
	leave = func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.unpark(id, s, w)
	}
	connectBy := now.Add(connectWait)
	if deadline.Before(connectBy) {
		connectBy = deadline
	}
	timer := time.NewTimer(connectBy.Sub(now))
	defer timer.Stop()
	for {
		switch {
		case !s.gone.IsZero():
			r.unpark(id, s, w)
			return nil, nil, enterGone
		case s.lc != nil && s.queue[0] == w:
			return s.lc, leave, enterReady
		}
		if w.wake == nil {
			w.wake = make(chan struct{})
		}
		ch := w.wake
		r.mu.Unlock()
		select {
		case <-ch:
			r.mu.Lock()
			continue
		case <-timer.C:
			r.mu.Lock()
			if s.lc != nil && connectBy.Before(deadline) {
				// Connected, and queued behind earlier clicks: only the wait
				// for the stream is capped, the turn in line runs to deadline.
				connectBy = deadline
				timer.Reset(time.Until(deadline))
				continue
			}
			r.unpark(id, s, w)
			return nil, nil, enterTimeout
		case <-ctx.Done():
			out = enterAbandoned
		case <-routerCtx.Done():
			out = enterClosed
		}
		r.mu.Lock()
		r.unpark(id, s, w)
		return nil, nil, out
	}
}

func (r *registry) unpark(id string, s *tabSlot, w *waiter) {
	for i, q := range s.queue {
		if q == w {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			r.parked--
			if i == 0 {
				s.signalHead()
			}
			break
		}
	}
	r.dropIfIdle(id, s)
}

// Tab ids are minted when a live page renders, so an action clicked before
// the stream connects already carries the id that stream will adopt. The id
// is 128 random bits plus a MAC over them, the mount, and the session cookie
// id the browser holds: a stream adopts only an id this app issued, for this
// mount, to the same credential it presents. Without the MAC a cross-site
// page could open a stream under an id it chose and then post actions naming
// it, and the id would stop being a synchronizer token.
//
// The key is per process, not the shared session key: only the pod holding
// a tab's stream can run its actions, so an id another pod issued is refused
// at once rather than parked for connectWait before the same 410.
const tabRandLen = 22 // randomToken's length

func (m *mount) newTabID(cred string) string {
	r := randomToken()
	return r + m.tabTag(r, cred)
}

// cred and r are base64url and r has a fixed length, so no mount path can
// shift the fields into one another.
func (m *mount) tabTag(r, cred string) string {
	mac := hmac.New(sha256.New, m.reg.key[:])
	mac.Write([]byte(m.patternBase + "\x00" + cred + "\x00" + r))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))[:tabRandLen]
}

func (m *mount) issuedTab(id, cred string) bool {
	if len(id) != 2*tabRandLen {
		return false
	}
	return hmac.Equal([]byte(id[tabRandLen:]), []byte(m.tabTag(id[:tabRandLen], cred)))
}

// tombFor is how long a tab id stays tombstoned once s ends: the pinned
// deadline when via ended it, nothing when the client went away and will
// retry under the same id.
func (m *mount) tombFor(s *stream) time.Duration {
	if s.serverEnded.Load() {
		return m.cfg.pinnedDeadline
	}
	return 0
}

// claimTab picks and reserves the id a connecting stream answers to.
func (m *mount) claimTab(sig map[string]json.RawMessage, cred string) string {
	var want string
	if raw, ok := sig[tabSignal]; ok {
		_ = json.Unmarshal(raw, &want)
	}
	if m.issuedTab(want, cred) && m.reg.claim(want) {
		return want
	}
	for {
		// Bound to cred like a rendered id, so a retry after a network drop,
		// which resends this id, gets its tab back.
		if id := m.newTabID(cred); m.reg.claim(id) {
			return id
		}
	}
}

// tabCredential is the session cookie id req carries, checked against its
// signature but not looked up: binding a tab id costs no store read.
func tabCredential(sm *sessionManager, req *http.Request) string {
	ck, err := req.Cookie(sm.cookie)
	if err != nil {
		return ""
	}
	id, ok := sm.verify(ck.Value)
	if !ok {
		return ""
	}
	return id
}

// pageTabCredential is the cookie id the browser will hold once this
// response lands: the one a render minted or rotated, else the request's.
func pageTabCredential(ctx *Ctx, sm *sessionManager, req *http.Request) string {
	if ctx.session != nil && ctx.session.id != "" {
		return ctx.session.id
	}
	return tabCredential(sm, req)
}
