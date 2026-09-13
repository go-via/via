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

// subStarter subscribes one ctx.Listen onto the connection's shared wake
// channel and returns the listener runStream then polls. via builds these in
// Listen; nothing here spawns a goroutine.
type subStarter func(wake chan struct{}) listener

// listener is one live subscription as runStream sees it, erased of its
// topic's element type. poll takes the whole queued backlog and returns the
// work item for it, or nil when there was nothing queued.
type listener struct{ poll func() func() }

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

// OnConnect registers fn to run once, when this unit's stream opens — the
// acquire half of OnDispose (join a room, claim a slot), and the only place for
// a connection-scoped side effect. OnInit itself runs on every request that
// renders the unit (a GET, an action, the SSE connect), so doing the acquire
// there directly would fire it on requests that never become a connection.
// Valid only inside OnInit. It does not by itself make a unit live: on a unit
// nothing else made live, fn never runs.
func (c *Ctx) OnConnect(fn func()) { c.onConnect = append(c.onConnect, fn) }

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
// Every published value reaches handler exactly once, in publish order, up to
// the subscription's queue limit (see topic.Publish for the one case that
// drops, which logs and is counted). Renders are NOT one per value: a backlog
// is handled in one batch — all its handler calls run, then a single re-render
// and a single SSE frame — so a burst costs frames proportional to how fast the
// client drains, never to how fast the topic publishes.
//
// The Subscribe itself is deferred to the moment the stream starts pumping:
// OnInit also runs on a plain GET and on every plain action, and
// subscribing there would hand out a Sub nothing will ever Stop.
func (c *Ctx) Listen[T any](t *topic.Topic[T], handler func(*Ctx, T)) {
	if c.initDone {
		log.Print("via: Listen called after OnInit returned — ignored; Listen is valid only inside OnInit")
		return
	}
	c.live = true
	c.subs = append(c.subs, func(wake chan struct{}) listener {
		// Both this call and runStream's disposer sweep run on the one
		// stream goroutine, the starter strictly before the sweep — so the
		// append needs no lock.
		sub := t.Subscribe()
		sub.Notify(wake)
		c.OnDispose(sub.Stop)
		return listener{poll: func() func() {
			batch, _ := sub.Drain()
			if len(batch) == 0 {
				return nil
			}
			// One push item per BATCH, not per value: every handler call
			// still runs, in publish order, but the re-render and its SSE
			// frame happen once for the whole backlog. Values published
			// while this item runs pile up for the next Drain, so a burst
			// costs a bounded number of frames instead of one each.
			// The item mutates THIS embed (c) and pushes only THIS embed's
			// container — so on a multiplex page a fan-out to one embed
			// never re-renders a sibling.
			return func() {
				for _, v := range batch {
					handler(c, v)
				}
				c.push()
			}
		}}
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
// fires when the incoming node is an Element. This is how a live embed's own
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

// stream serializes every write to one stream and tears the stream
// down on the FIRST write or flush failure. A half-open peer (vanished without a
// FIN) never cancels the request context, so a failed frame write is the only
// in-band signal it's gone: on failure stream cancels the stream's context,
// which stops the embed goroutine, its tickers, and its subscriptions and runs
// disposers — instead of leaking them against a dead socket. A per-frame write
// deadline (timeout) keeps a stalled-but-alive peer from pinning the single
// goroutine forever — timeout is always positive (WithSSEWriteTimeout
// rejects otherwise). All calls run on the embed goroutine, so it needs no
// lock.
type stream struct {
	w       io.Writer
	rc      *http.ResponseController
	timeout time.Duration
	cancel  context.CancelFunc
	failed  bool
}

// frame emits one SSE event through write, flushes it, and on any write/flush
// error cancels the stream so the embed tears down. Once failed it is a no-op,
// so a write racing a just-torn-down stream can't re-trigger teardown.
func (s *stream) frame(write func(io.Writer)) {
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

// runStream drives one or more live units on a single goroutine. Every unit's
// ticks, subscriptions, dispatched actions (via tabStream.run), AND the
// keepalive feed through this one goroutine — so all mutation, render, and stream
// writes are serialized, no lock. Each push item is self-contained: it mutates
// its embed and pushes only that embed's container (via ctx.push), so a
// multiplex page's embeds stay independent. It always loops (even with no
// ticks/subs) so an interactive-only embed still receives dispatched actions and
// beats; every embed's disposers run on exit (client disconnect or a failed
// write).
func runStream(reqCtx context.Context, embeds []*Ctx, pushq chan func(), keepalive func(), interval time.Duration) {
	defer func() {
		for _, embed := range embeds {
			for _, d := range embed.disposers {
				// runPushItem, not a bare call: a disposer is user code (e.g.
				// sub.Stop) and one panicking must not skip every disposer after
				// it — that would leak whatever the rest were meant to release.
				runPushItem(d)
			}
		}
	}()
	// One wake channel for the whole connection, shared by every
	// subscription on it: a Listen used to cost a goroutine per subscription
	// per connection (8KB of stack each, ~15k goroutines at 5000 tabs x 3
	// listens) purely to bridge its Ready channel onto this select. Capacity
	// 1 and coalescing, so a publisher never blocks and N pending values
	// still cost one sweep.
	wake := make(chan struct{}, 1)
	var listeners []listener
	for _, embed := range embeds {
		for _, t := range embed.ticks {
			startTicker(reqCtx, embed, t, pushq)
		}
		for _, start := range embed.subs {
			listeners = append(listeners, start(wake))
		}
	}
	// One token may stand for any number of subscriptions, so every wake
	// sweeps them ALL — in registration order, which makes handler ordering
	// across two Listens on one unit deterministic instead of a race between
	// two reader goroutines.
	sweep := func() {
		for _, l := range listeners {
			if work := l.poll(); work != nil {
				runPushItem(work)
			}
		}
	}
	beat := time.NewTicker(interval)
	defer beat.Stop()
	for {
		select {
		case <-reqCtx.Done():
			return
		case fn := <-pushq:
			runPushItem(fn)
		case <-wake:
			sweep()
		case <-beat.C:
			runPushItem(keepalive)
		}
	}
}

// runPushItem runs one push item (a tick's fn+push, a Listen handler+push,
// or a dispatched action's mutation+pushWork) with its own recover, so a panic
// in one bad render — e.g. a View that only fails for a particular Tick
// value — logs and drops that item instead of unwinding runStream: an
// action's result is already sent to the waiting POST by the time pushWork
// runs, so without this the stream would die silently after a 204 the client
// already saw as success.
func runPushItem(fn func()) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("via: live push panic: %v\n%s", rec, debug.Stack())
		}
	}()
	fn()
}

// tabStream is a connected tab's live units, kept in the per-Register registry
// so a POST action can be routed onto its single goroutine. units is
// replaced after every push with that render's bind Ctx — its actions and
// hydrators table is what a live action needs, and it is always the render
// the client's DOM currently reflects.
type tabStream struct {
	mount       *mount            // the mount that opened this connection — a tab id is only valid on ITS mount, never another sharing the router-wide registry
	pushq       chan func()       // the connection's serialization channel (shared by all its units)
	done        <-chan struct{}   // reqCtx.Done() — closed on disconnect
	pushSignals func(json string) // emit a patch-signals frame on this stream
	mu          sync.Mutex        // guards units: replace runs on the embed goroutine, unit is read from the dispatching request's own goroutine
	units       map[string]*Ctx   // dispatch address ("r"=root, the embed key for an embedded unit) → current unit Ctx, at any embedding depth
	sess        *sessionData      // the session, if any, that was resolved from the connect request's cookie — nil for an anonymous connect. Compared by pointer, not id, so it survives a later Session.Rotate (reID moves the same *sessionData to a fresh id; it never changes the pointer)
}

// boundSession returns the session this connection is bound to, if any.
// Guarded because a live action can bind it after connect (see bindSession),
// racing a concurrent dispatch's read on its own goroutine.
func (c *tabStream) boundSession() *sessionData {
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
func (c *tabStream) bindSession(d *sessionData) {
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
func (c *tabStream) replace(u *Ctx) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.units[unitAddr(u)] = u
}

// run posts fn onto the embed goroutine, where it runs serialized with
// ticks/subs/renders, and WAITS for its actionResult — synchronous, so a live
// action's Redirect, session cookie, and panic all resolve on the POST that
// triggered it. fn itself only runs the mutation (see liveRunAction); the
// result channel is buffered so a late send (once fn is finally dequeued)
// never blocks a goroutine that already gave up. Every wait is select-guarded
// on both c.done (the connection closed) and reqCtx (the POST itself gave up
// or was canceled) so a POST racing a just-closed tab, or one whose own
// deadline fires while the embed goroutine is busy with something else
// entirely, returns ok=false (the caller answers 410) instead of blocking
// forever.
//
// res.pushWork (the dirty-signals + element push) runs AFTER result is sent,
// still on this same embed goroutine: the waiting POST is free to proceed
// the instant result is sent (result is buffered, so the send itself never
// blocks), while pushWork stays serialized with every other push item in the
// exact order its mutation ran — a detached goroutine doing this instead would
// race other actions' detached goroutines and could push out of order.
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

// registry maps a per-connection tab id to its live embed. It is a local of
// each Register call (never global): one app, one registry.
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

// writeSignalsFrame writes one Datastar patch-signals SSE event. via uses it to
// hand the client its per-connection tab id as the local signal _viatab (the
// underscore keeps Datastar from echoing it in POST bodies; it rides the
// X-Via-Tab header instead).
func writeSignalsFrame(w io.Writer, signalsJSON string) {
	_, _ = io.WriteString(w, "event: datastar-patch-signals\ndata: signals ")
	_, _ = io.WriteString(w, signalsJSON)
	_, _ = io.WriteString(w, "\n\n")
}

func startTicker(reqCtx context.Context, embed *Ctx, t tickReg, pushq chan<- func()) {
	go func() {
		tk := time.NewTicker(t.d)
		defer tk.Stop()
		for {
			select {
			case <-reqCtx.Done():
				return
			case <-tk.C:
				// Self-contained unit: mutate this embed, then push only its
				// container — so a tick in one embed never re-renders a sibling.
				select {
				case pushq <- func() { t.fn(embed); embed.push() }:
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
