package via

import (
	"context"
	"log"
	"runtime/debug"
	"sync"
)

// tabStream is a connected tab's live units, kept in the registry so a POST
// action routes onto its single goroutine. units is replaced after every push
// with that render's bind Ctx — always the render the client's DOM reflects.
type tabStream struct {
	mount       *mount            // a tab id is valid only on ITS mount, never another sharing the router-wide registry
	pushq       chan func()       // serialization channel, shared by all this connection's units
	done        <-chan struct{}   // closed on disconnect
	pushSignals func(json string) // emit a patch-signals frame on this stream
	mu          sync.Mutex        // replace runs on the embed goroutine, unit is read from the dispatching request's
	units       map[string]*Ctx   // dispatch address → current unit Ctx, at any embedding depth
	sess        string            // the bound session's stable sid; "" when anonymous. An sid, not the cookie id, so a Rotate (which re-ids) and another pod both still match
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

// replace registers u as the current bind for its dispatch address, so the
// next action targets this render's actions/hydrators table.
func (c *tabStream) replace(u *Ctx) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.units[unitAddr(u)] = u
}

// run posts fn onto the embed goroutine and WAITS for its actionResult, so a
// live action's Redirect, session cookie and panic all resolve on the POST that
// triggered it. The result channel is buffered so a late send never blocks a
// goroutine that already gave up, and every wait is guarded on both c.done and
// reqCtx so a POST racing a closed tab, or one whose deadline fires while the
// goroutine is busy elsewhere, returns false (→ 410) instead of blocking.
//
// res.pushWork runs AFTER result is sent, still on this goroutine: the POST
// proceeds at once while pushWork stays serialized in the order its mutation
// ran. A detached goroutine would race other actions' and push out of order.
func (c *tabStream) run(reqCtx context.Context, fn func() actionResult) (actionResult, bool) {
	result := make(chan actionResult, 1)
	select {
	case c.pushq <- func() {
		var res actionResult
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("via: live action panic: %v\n%s", rec, debug.Stack())
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
		return actionResult{}, false
	case <-reqCtx.Done():
		return actionResult{}, false
	}
	select {
	case res := <-result:
		return res, true
	case <-c.done:
		return actionResult{}, false
	case <-reqCtx.Done():
		return actionResult{}, false
	}
}

// registry maps a per-connection tab id to its live embed. A local of each
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
