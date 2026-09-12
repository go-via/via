package via

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/go-via/via/topic"
)

type tickReg struct {
	d  time.Duration
	fn func(*Ctx)
}

// subStarter spawns one subscription's reader goroutine, bridging an external
// channel into the island's single pulse loop. via builds these in Listen.
type subStarter func(reqCtx context.Context, pulse chan<- func())

// Tick schedules fn to run every d for the life of the unit's connection, and
// is one of the two things that make a unit LIVE (rendering a State or List is
// the other). fn is a named method value (e.g. c.beat); after each run via
// re-renders the unit and pushes an element-patch over the SSE stream. Valid
// only inside OnInit — the ticks/subs registered there are snapshotted once,
// so a call after OnInit returns registers nothing and logs loudly instead of
// silently doing nothing.
func (c *Ctx) Tick(d time.Duration, fn func(*Ctx)) {
	if c.initDone {
		log.Print("via: Tick called after OnInit returned — ignored; Tick is valid only inside OnInit")
		return
	}
	c.live = true
	c.ticks = append(c.ticks, tickReg{d: d, fn: fn})
}

// OnLive registers fn to run once, when this unit's live connection opens — the
// acquire half of OnDispose (join a room, claim a slot), and the only place for
// a connection-scoped side effect. OnInit itself runs on every request that
// renders the unit (a GET, an action, the SSE connect), so doing the acquire
// there directly would fire it on requests that never become a connection.
// Valid only inside OnInit. It does not by itself make a unit live: on a unit
// nothing else made live, fn never runs.
func (c *Ctx) OnLive(fn func()) { c.onLive = append(c.onLive, fn) }

// OnDispose registers a teardown function run when the unit's connection
// closes — stop subscriptions, release producers. fn is a named method value
// (e.g. sub.Stop). Valid only inside OnInit, and only meaningful on a unit
// something else has already made live: a unit that never ticks, listens, or
// renders State opens no connection to tear down.
func (c *Ctx) OnDispose(fn func()) { c.disposers = append(c.disposers, fn) }

// Listen wires a unit to a Topic in one line: it subscribes, pumps every
// published value into handler on the unit's own goroutine (serialized with
// Tick, so unit state is mutated race-free), pushes this unit's re-render, and
// stops the subscription on disconnect. Like Tick it makes the unit live, and
// is valid only inside OnInit — see Tick for why a call after OnInit returns is
// a loud no-op.
//
// The Subscribe itself is deferred to the moment the stream starts pumping:
// OnInit also runs on a plain GET and on every stateless action, and
// subscribing there would hand out a Sub nothing will ever Stop.
func (c *Ctx) Listen[T any](t *topic.Topic[T], handler func(*Ctx, T)) {
	if c.initDone {
		log.Print("via: Listen called after OnInit returned — ignored; Listen is valid only inside OnInit")
		return
	}
	c.live = true
	c.subs = append(c.subs, func(reqCtx context.Context, pulse chan<- func()) {
		// Both this call and runLiveStream's disposer sweep run on the one
		// stream goroutine, the starter strictly before the sweep — so the
		// append needs no lock.
		sub := t.Subscribe()
		c.OnDispose(sub.Stop)
		go func() {
			ch := sub.C()
			for {
				select {
				case <-reqCtx.Done():
					return
				case v, ok := <-ch:
					if !ok {
						return
					}
					// The enqueued unit mutates THIS island (c) and pushes only
					// THIS island's container — so on a multiplex page a fan-out to
					// one island never re-renders a sibling.
					select {
					case pulse <- func() { handler(c, v); c.push() }:
					case <-reqCtx.Done():
						return
					}
				}
			}
		}()
	})
}

// writePatchFrame writes one Datastar element-patch SSE event. The fragment is
// the re-rendered <div id="root">…</div>, which the client morphs into the live
// DOM by id (default mode).
func writePatchFrame(w io.Writer, fragment []byte) {
	_, _ = io.WriteString(w, "event: datastar-patch-elements\n")
	writeElementLines(w, fragment)
	_, _ = io.WriteString(w, "\n")
}

// writeInnerPatchFrame writes one Datastar element-patch SSE event that patches
// the CHILDREN of #id, never comparing id's own element — mode inner hands the
// client a DocumentFragment, and the both-sided data-ignore-morph check only
// fires when the incoming node is an Element. This is how a live island's own
// push still lands on a container a root-walk render has marked
// data-ignore-morph to keep a parent's patch from repainting it.
func writeInnerPatchFrame(w io.Writer, id string, fragment []byte) {
	_, _ = io.WriteString(w, "event: datastar-patch-elements\n")
	_, _ = io.WriteString(w, "data: selector #"+id+"\n")
	_, _ = io.WriteString(w, "data: mode inner\n")
	writeElementLines(w, fragment)
	_, _ = io.WriteString(w, "\n")
}

// writeElementLines emits fragment as one `data: elements` field per physical
// line: a bare newline in rendered content (e.g. a value carrying "\n") would
// otherwise split the SSE event and truncate the patch. The client rejoins the
// multi-line payload with newlines, reconstructing it.
func writeElementLines(w io.Writer, fragment []byte) {
	for line := range bytes.SplitSeq(fragment, []byte{'\n'}) {
		_, _ = io.WriteString(w, "data: elements ")
		_, _ = w.Write(line)
		_, _ = io.WriteString(w, "\n")
	}
}

// writeKeepaliveFrame writes one SSE comment frame. A comment (a line starting
// with ':') is ignored by the client — it mutates no signal and patches no
// element — so it keeps the connection warm and proves liveness without
// touching client state. Its real job is the write itself: a failure on a
// vanished (half-open) peer is what lets the stream tear down instead of leaking.
func writeKeepaliveFrame(w io.Writer) {
	_, _ = io.WriteString(w, ": keepalive\n\n")
}

// errWriter records the first write error so a frame's many small writes can be
// checked once, after the whole frame is emitted.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.w.Write(p)
	if err != nil {
		e.err = err
	}
	return n, err
}

// sseStream serializes every write to one live connection and tears the stream
// down on the FIRST write or flush failure. A half-open peer (vanished without a
// FIN) never cancels the request context, so a failed frame write is the only
// in-band signal it's gone: on failure sseStream cancels the stream's context,
// which stops the island goroutine, its tickers, and its subscriptions and runs
// disposers — instead of leaking them against a dead socket. A per-frame write
// deadline (timeout) keeps a stalled-but-alive peer from pinning the single
// goroutine forever — timeout is always positive (WithSSEWriteTimeout
// rejects otherwise). All calls run on the island goroutine, so it needs no
// lock.
type sseStream struct {
	w       io.Writer
	rc      *http.ResponseController
	timeout time.Duration
	cancel  context.CancelFunc
	failed  bool
}

// frame emits one SSE event through write, flushes it, and on any write/flush
// error cancels the stream so the island tears down. Once failed it is a no-op,
// so a write racing a just-torn-down stream can't re-trigger teardown.
func (s *sseStream) frame(write func(io.Writer)) {
	if s.failed {
		return
	}
	if s.timeout > 0 {
		_ = s.rc.SetWriteDeadline(time.Now().Add(s.timeout))
	}
	ew := &errWriter{w: s.w}
	write(ew)
	err := ew.err
	if err == nil {
		err = s.rc.Flush()
	}
	if err != nil {
		s.failed = true
		s.cancel()
	}
}

// runLiveStream drives one or more live units on a single goroutine. Every unit's
// ticks, subscriptions, dispatched actions (via liveConn.run), AND the
// keepalive feed through this one goroutine — so all mutation, render, and stream
// writes are serialized, no lock. Each pulse unit is self-contained: it mutates
// its island and pushes only that island's container (via ctx.push), so a
// multiplex page's islands stay independent. It always loops (even with no
// ticks/subs) so an interactive-only island still receives dispatched actions and
// beats; every island's disposers run on exit (client disconnect or a failed
// write).
func runLiveStream(reqCtx context.Context, islands []*Ctx, pulse chan func(), keepalive func(), interval time.Duration) {
	defer func() {
		for _, island := range islands {
			for _, d := range island.disposers {
				// runPulseItem, not a bare call: a disposer is user code (e.g.
				// sub.Stop) and one panicking must not skip every disposer after
				// it — that would leak whatever the rest were meant to release.
				runPulseItem(d)
			}
		}
	}()
	for _, island := range islands {
		for _, t := range island.ticks {
			startTicker(reqCtx, island, t, pulse)
		}
		for _, start := range island.subs {
			start(reqCtx, pulse)
		}
	}
	beat := time.NewTicker(interval)
	defer beat.Stop()
	for {
		select {
		case <-reqCtx.Done():
			return
		case fn := <-pulse:
			runPulseItem(fn)
		case <-beat.C:
			runPulseItem(keepalive)
		}
	}
}

// runPulseItem runs one pulse item (a tick's fn+push, a Listen handler+push,
// or a dispatched action's mutation+pushWork) with its own recover, so a panic
// in one bad render — e.g. a View that only fails for a particular Tick
// value — logs and drops that item instead of unwinding runLiveStream: an
// action's result is already sent to the waiting POST by the time pushWork
// runs, so without this the stream would die silently after a 204 the client
// already saw as success.
func runPulseItem(fn func()) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("via: live pulse panic: %v\n%s", rec, debug.Stack())
		}
	}()
	fn()
}

// liveConn is a connected tab's live units, kept in the per-Register registry
// so a POST action can be routed onto its single goroutine. units is
// replaced after every push with that render's bind Ctx — its actions and
// hydrators table is what a live action needs, and it is always the render
// the client's DOM currently reflects.
type liveConn struct {
	mount       *mount            // the mount that opened this connection — a tab id is only valid on ITS mount, never another sharing the router-wide registry
	pulse       chan func()       // the connection's serialization channel (shared by all its units)
	done        <-chan struct{}   // reqCtx.Done() — closed on disconnect
	pushSignals func(json string) // emit a patch-signals frame on this stream
	mu          sync.Mutex        // guards units: replace runs on the island goroutine, unit is read from the dispatching request's own goroutine
	units       map[int]*Ctx      // dispatch address (0=root, islandIdx+1=embedded) → current unit Ctx, at any embedding depth
	sess        *sessionData      // the session, if any, that was resolved from the connect request's cookie — nil for an anonymous connect. Compared by pointer, not id, so it survives a later Session.Rotate (reID moves the same *sessionData to a fresh id; it never changes the pointer)
}

// boundSession returns the session this connection is bound to, if any.
// Guarded because a live action can bind it after connect (see bindSession),
// racing a concurrent dispatch's read on its own goroutine.
func (c *liveConn) boundSession() *sessionData {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess
}

// bindSession records d as this connection's credential on first mint — a
// connect-time cookie already resolved before the connection is published to
// the registry, or (the gap this closes) a session a live action's
// Session().Put/Rotate establishes after connect, when nothing bound it yet.
// Idempotent: once bound, later calls (e.g. a subsequent Rotate on the same
// connection) are no-ops here — dispatch compares by pointer, and reID moves
// the same *sessionData to a fresh id without changing the pointer.
func (c *liveConn) bindSession(d *sessionData) {
	if d == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess == nil {
		c.sess = d
	}
}

// replace registers u as the current bind for its own dispatch address —
// the next action against it, or a full-page re-render's Embed reusing its
// already-connected instance, targets this render's actions/hydrators table.
func (c *liveConn) replace(u *Ctx) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.units[unitAddr(u)] = u
}

// run posts fn onto the island goroutine, where it runs serialized with
// ticks/subs/renders, and WAITS for its actionResult — synchronous, so a live
// action's Redirect, session cookie, and panic all resolve on the POST that
// triggered it. fn itself only runs the mutation (see liveRunAction); the
// result channel is buffered so a late send (once fn is finally dequeued)
// never blocks a goroutine that already gave up. Every wait is select-guarded
// on both c.done (the connection closed) and reqCtx (the POST itself gave up
// or was canceled) so a POST racing a just-closed tab, or one whose own
// deadline fires while the island goroutine is busy with something else
// entirely, returns ok=false (the caller answers 410) instead of blocking
// forever.
//
// res.pushWork (the dirty-signals + element push) runs AFTER result is sent,
// still on this same island goroutine: the waiting POST is free to proceed
// the instant result is sent (result is buffered, so the send itself never
// blocks), while pushWork stays serialized with every other pulse item in the
// exact order its mutation ran — a detached goroutine doing this instead would
// race other actions' detached goroutines and could push out of order.
func (c *liveConn) run(reqCtx context.Context, fn func() actionResult) (actionResult, bool) {
	result := make(chan actionResult, 1)
	select {
	case c.pulse <- func() {
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

// registry maps a per-connection tab id to its live island. It is a local of
// each Register call (never global): one app, one registry.
type registry struct {
	mu sync.Mutex
	m  map[string]*liveConn
}

func newRegistry() *registry { return &registry{m: make(map[string]*liveConn)} }

func (r *registry) put(id string, c *liveConn) {
	r.mu.Lock()
	r.m[id] = c
	r.mu.Unlock()
}

func (r *registry) get(id string) (*liveConn, bool) {
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

// writeSignalsFrame writes one Datastar patch-signals SSE event. via uses it to
// hand the client its per-connection tab id as the local signal _viatab (the
// underscore keeps Datastar from echoing it in POST bodies; it rides the
// X-Via-Tab header instead).
func writeSignalsFrame(w io.Writer, signalsJSON string) {
	_, _ = io.WriteString(w, "event: datastar-patch-signals\ndata: signals ")
	_, _ = io.WriteString(w, signalsJSON)
	_, _ = io.WriteString(w, "\n\n")
}

func startTicker(reqCtx context.Context, island *Ctx, t tickReg, pulse chan<- func()) {
	go func() {
		tk := time.NewTicker(t.d)
		defer tk.Stop()
		for {
			select {
			case <-reqCtx.Done():
				return
			case <-tk.C:
				// Self-contained unit: mutate this island, then push only its
				// container — so a tick in one island never re-renders a sibling.
				select {
				case pulse <- func() { t.fn(island); island.push() }:
				case <-reqCtx.Done():
					return
				}
			}
		}
	}()
}

// writeSSEHeaders sets the headers for an event stream. nosniff guards the
// stream like the other responses; Cache-Control and the flush keep proxies and
// the browser from buffering frames.
func writeSSEHeaders(w http.ResponseWriter) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("X-Content-Type-Options", "nosniff")
}
