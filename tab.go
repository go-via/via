package via

import (
	"context"
	"encoding/json"
	"log"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// tabStream is a connected tab's live units, kept in the registry so a POST
// action routes onto its single goroutine. units is replaced after every push
// with that render's bind Ctx — always the render the client's DOM reflects.
type tabStream struct {
	mount       *mount            // a tab id is valid only on ITS mount, never another sharing the router-wide registry
	pushq       chan func()       // serialization channel, shared by all this connection's units
	done        <-chan struct{}   // closed on disconnect
	pushSignals func(json string) // emit a patch-signals frame on this stream
	mu          sync.Mutex        // replace runs on the child goroutine, unit is read from the dispatching request's
	units       map[string]*Ctx   // dispatch address → current unit Ctx, at any embedding depth
	sess        string            // the bound session's stable sid; "" when anonymous. An sid, not the cookie id, so a Rotate (which re-ids) and another pod both still match

	// client and rev are touched only on this connection's own goroutine —
	// connect builds them before runStream, and every push and every live
	// action reaches them through pushq — so neither takes mu.
	client map[string]json.RawMessage // the slots the client last posted, re-applied to every DISPLAY render (livePush)
	rev    *revertSet                 // how to undo that application before the next AUTHORITY render

	id           string      // the per-connection tab id, for correlating a log line with a tab
	pinnedLogged atomic.Bool // warnPinned is once per connection, not once per click
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

// sid is the stable identity of s's session, "" when there is none.
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
// It runs BEFORE the push's renders so the patch-signals frame precedes the
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

// runOutcome says WHY a dispatch did not produce a result. One 410 for three
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
// it before declaring the goroutine pinned. Well past any sane handler, and
// short enough to answer before a load balancer or client deadline does — the
// old code waited on req.Context() alone, so the pinned case was invisible and
// arrived as a 410 that blamed the client.
var pinnedDeadline = 5 * time.Second

// run posts fn onto the child goroutine and WAITS for its actionResult, so a
// live action's Redirect, session cookie and panic all resolve on the POST that
// triggered it. The result channel is buffered so a late send never blocks a
// goroutine that already gave up, and every wait is guarded on c.done, reqCtx
// and pinnedDeadline so a POST racing a closed tab, one whose client hung up,
// and one whose goroutine never arrives are told apart rather than collapsed.
//
// res.pushWork runs AFTER result is sent, still on this goroutine: the POST
// proceeds at once while pushWork stays serialized in the order its mutation
// ran. A detached goroutine would race other actions' and push out of order.
func (c *tabStream) run(reqCtx context.Context, fn func() actionResult) (actionResult, runOutcome) {
	result := make(chan actionResult, 1)
	pinned := time.NewTimer(pinnedDeadline)
	defer pinned.Stop()
	select {
	case c.pushq <- func() {
		var res actionResult
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("via: live action panic [tab=%s unit=%s]: %v\n%s", c.id, c.unitType(), rec, debug.Stack())
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
		return actionResult{}, runClosed
	case <-reqCtx.Done():
		return actionResult{}, runAbandoned
	case <-pinned.C:
		c.warnPinned()
		return actionResult{}, runPinned
	}
	select {
	case res := <-result:
		return res, runOK
	case <-c.done:
		return actionResult{}, runClosed
	case <-reqCtx.Done():
		return actionResult{}, runAbandoned
	case <-pinned.C:
		c.warnPinned()
		return actionResult{}, runPinned
	}
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

// warnPinned fires once per connection: a pinned goroutine stays pinned, and
// every subsequent click would repeat the line. Keepalives stop too
// (runStream's beat is on the same goroutine), so the connection is torn down
// by a proxy and silently leaked for the life of the process — this line is the
// only signal that happened.
func (c *tabStream) warnPinned() {
	if c.pinnedLogged.Swap(true) {
		return
	}
	log.Printf("via: live action queue not drained within %s [tab=%s unit=%s] — the connection's goroutine is "+
		"blocked inside a Tick, Listen or action handler, so its keepalives have stopped too and every action on "+
		"this tab answers 503 until it returns. Move blocking work off the handler.",
		pinnedDeadline, c.id, c.unitType())
}

// registry maps a per-connection tab id to its live child. A local of each
// Handler call, never global.
type registry struct {
	mu sync.Mutex
	m  map[string]*tabStream
}

func newRegistry() *registry { return &registry{m: make(map[string]*tabStream)} }

func (r *registry) put(id string, c *tabStream) {
	r.mu.Lock()
	r.m[id] = c
	r.mu.Unlock()
}

func (r *registry) get(id string) (*tabStream, bool) {
	r.mu.Lock()
	c, ok := r.m[id]
	r.mu.Unlock()
	return c, ok
}

func (r *registry) del(id string) {
	r.mu.Lock()
	delete(r.m, id)
	r.mu.Unlock()
}
