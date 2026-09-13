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
// channel and returns the listener runStream polls. Spawns no goroutine.
type subStarter func(wake chan struct{}) listener

// listener is one live subscription, erased of its topic's element type. poll
// takes the whole queued backlog and returns its work item, or nil.
type listener struct{ poll func() func() }

// Tick schedules fn to run every d for the life of the unit's connection, and
// is one of the two things that make a unit LIVE (rendering a State or List is
// the other). After each run via re-renders the unit and pushes an
// element-patch. Valid only inside OnInit: ticks and subs are snapshotted
// there, so a later call registers nothing and logs loudly.
func (c *Ctx) Tick(d time.Duration, fn func(*Ctx)) {
	if c.reinit {
		return // the post-action re-run; this unit's ticks were snapshotted at GET/connect (I5)
	}
	if c.initDone {
		log.Print("via: Tick called after OnInit returned — ignored; Tick is valid only inside OnInit")
		return
	}
	c.live = true
	c.ticks = append(c.ticks, tickReg{d: d, fn: fn})
}

// OnConnect registers fn to run once when this unit's stream opens — the
// acquire half of OnDispose (join a room, claim a slot). OnInit itself runs on
// every request that renders the unit, so an acquire there would fire on
// requests that never become a connection. Valid only inside OnInit, and it
// does not itself make a unit live: on a unit nothing else made live, fn never
// runs.
func (c *Ctx) OnConnect(fn func()) { c.onConnect = append(c.onConnect, fn) }

// OnDispose registers a teardown function run when the unit's connection
// closes — stop subscriptions, release producers. fn is a named method value
// (e.g. sub.Stop). Valid only inside OnInit, and only meaningful on a unit
// something else has already made live: a unit that never ticks, listens, or
// renders State opens no connection to tear down.
func (c *Ctx) OnDispose(fn func()) { c.disposers = append(c.disposers, fn) }

// Listen wires a unit to a Topic: it subscribes, pumps every published value
// into handler on the unit's own goroutine (serialized with Tick, so unit state
// is mutated race-free), pushes this unit's re-render, and stops on disconnect.
// Like Tick it makes the unit live and is valid only inside OnInit.
//
// Every published value reaches handler exactly once, in publish order, up to
// the subscription's queue limit (see topic.Publish for the one drop case,
// which logs; Listen owns the Sub, so hold your own Subscribe to read
// Sub.Dropped). Renders are NOT one per value — a whole backlog runs its
// handler calls, then one re-render and one SSE frame — so a burst costs frames
// proportional to how fast the client drains, not how fast the topic publishes.
//
// The Subscribe is deferred to the moment the stream starts pumping: OnInit
// also runs on a plain GET and every plain action, and subscribing there would
// hand out a Sub nothing will ever Stop.
func (c *Ctx) Listen[T any](t *topic.Topic[T], handler func(*Ctx, T)) {
	if c.reinit {
		return // see Tick
	}
	if c.initDone {
		log.Print("via: Listen called after OnInit returned — ignored; Listen is valid only inside OnInit")
		return
	}
	c.live = true
	c.subs = append(c.subs, func(wake chan struct{}) listener {
		// This call and runStream's disposer sweep both run on the one stream
		// goroutine, the starter strictly first, so the append needs no lock.
		sub := t.Subscribe()
		sub.WakeOn(wake)
		c.OnDispose(sub.Stop)
		return listener{poll: func() func() {
			batch, _ := sub.Drain()
			if len(batch) == 0 {
				return nil
			}
			// One push item per BATCH: values published while this item runs
			// pile up for the next Drain, so a burst costs a bounded number of
			// frames. The item pushes only THIS embed's container, so a fan-out
			// never re-renders a sibling.
			return func() {
				// One recover per VALUE, and the push runs regardless: the
				// batch is a frame-rate optimisation, not a failure domain, so
				// a panic on value k must not swallow k+1..n or the re-render
				// the surviving values earned.
				for _, v := range batch {
					callListener(c, handler, v)
				}
				c.push()
			}
		}}
	})
}

// callListener recovers per call, so a panicking handler does not tear down
// the connection's push goroutine.
func callListener[T any](c *Ctx, handler func(*Ctx, T), v T) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("via: panic in a Listen handler: %v\n%s", r, debug.Stack())
		}
	}()
	handler(c, v)
}

// writePatchFrame writes one Datastar element-patch SSE event; the client
// morphs the fragment into the live DOM by id (default mode).
func writePatchFrame(w io.Writer, fragment []byte) {
	_, _ = io.WriteString(w, "event: datastar-patch-elements\n")
	writeElementLines(w, fragment)
	_, _ = io.WriteString(w, "\n")
}

// writeInnerPatchFrame patches the CHILDREN of #id, never comparing id's own
// element: mode inner hands the client a DocumentFragment, and the both-sided
// data-ignore-morph check only fires when the incoming node is an Element. That
// is how a live embed's push still lands on a container the root-walk render
// marked data-ignore-morph.
func writeInnerPatchFrame(w io.Writer, id string, fragment []byte) {
	_, _ = io.WriteString(w, "event: datastar-patch-elements\n")
	_, _ = io.WriteString(w, "data: selector #"+id+"\n")
	_, _ = io.WriteString(w, "data: mode inner\n")
	writeElementLines(w, fragment)
	_, _ = io.WriteString(w, "\n")
}

// writeElementLines emits one `data: elements` field per physical line: a bare
// newline in rendered content would otherwise split the SSE event and truncate
// the patch. The client rejoins them.
func writeElementLines(w io.Writer, fragment []byte) {
	for line := range bytes.SplitSeq(fragment, []byte{'\n'}) {
		_, _ = io.WriteString(w, "data: elements ")
		_, _ = w.Write(line)
		_, _ = io.WriteString(w, "\n")
	}
}

// writeKeepaliveFrame writes one SSE comment frame, which the client ignores
// entirely. Its real job is the write itself: a failure on a vanished
// (half-open) peer is what lets the stream tear down instead of leaking.
func writeKeepaliveFrame(w io.Writer) {
	_, _ = io.WriteString(w, ": keepalive\n\n")
}

// errWriter records the first write error so a frame's many small writes are
// checked once, at the end.
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

// stream serializes every write and tears the stream down on the FIRST write
// or flush failure. A half-open peer (vanished without a FIN) never cancels the
// request context, so a failed frame write is the only in-band signal it's
// gone; cancelling stops the embed goroutine, its tickers and subscriptions and
// runs disposers instead of leaking them against a dead socket. The per-frame
// deadline keeps a stalled-but-alive peer from pinning the goroutine forever.
// All calls run on the embed goroutine, so it needs no lock.
type stream struct {
	w       io.Writer
	rc      *http.ResponseController
	timeout time.Duration
	cancel  context.CancelFunc
	failed  bool
}

// frame emits one SSE event and flushes it, cancelling the stream on any
// error. Once failed it is a no-op, so a write racing a just-torn-down stream
// can't re-trigger teardown.
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

// abort tears the connection down from inside a push — the one exit a render
// that cannot succeed has, since a push carries no response to answer with.
func (s *stream) abort() {
	s.failed = true
	s.cancel()
}

// runStream drives one or more live units on a single goroutine: every unit's
// ticks, subscriptions, dispatched actions and the keepalive feed through it,
// so all mutation, render and stream writes are serialized without a lock. Each
// push item pushes only its own embed's container. It always loops, even with
// no ticks or subs, so an interactive-only embed still receives actions and
// beats; disposers run on exit.
func runStream(reqCtx context.Context, embeds []*Ctx, pushq chan func(), keepalive func(), interval time.Duration) {
	defer func() {
		for _, embed := range embeds {
			for _, d := range embed.disposers {
				// runPushItem, not a bare call: a disposer is user code, and one
				// panicking must not skip the rest and leak what they release.
				runPushItem(d)
			}
		}
	}()
	// One wake channel shared by every subscription: a Listen used to cost a
	// goroutine per subscription per connection (8KB of stack each, ~15k
	// goroutines at 5000 tabs x 3 listens) purely to bridge its Ready channel
	// onto this select. Capacity 1 and coalescing, so a publisher never blocks
	// and N pending values still cost one sweep.
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
	// One token may stand for any number of subscriptions, so every wake sweeps
	// them ALL, in registration order — which makes handler ordering across two
	// Listens deterministic instead of a race between two reader goroutines.
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

// runPushItem recovers per item, so one bad render logs and drops that item
// instead of unwinding runStream: an action's result is already sent to the
// waiting POST by the time pushWork runs, so without this the stream would die
// silently after a 204 the client already saw as success.
func runPushItem(fn func()) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("via: live push panic: %v\n%s", rec, debug.Stack())
		}
	}()
	fn()
}

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

// bindSession records sid as this connection's credential on first mint — the
// connect cookie, or (the gap this closes) a session a live action established
// after connect. Idempotent: a later Rotate is a no-op here, since the sid
// survives it.
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

// writeSignalsFrame writes one Datastar patch-signals SSE event — via uses it
// to hand the client its tab id, whose name must stay underscore-free (see
// tabSignal).
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
				// Push only this embed's container, so a tick never re-renders
				// a sibling.
				select {
				case pushq <- func() { t.fn(embed); embed.push() }:
				case <-reqCtx.Done():
					return
				}
			}
		}
	}()
}

// writeSSEHeaders sets the event-stream headers; Cache-Control keeps proxies
// and the browser from buffering frames.
func writeSSEHeaders(w http.ResponseWriter) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("X-Content-Type-Options", "nosniff")
}
