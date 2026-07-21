package via

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-via/via/topic"
)

// Live marks a composition as a connection-scoped live island: implementing
// OnConnect opts it into server-held state and a server-push SSE stream. It is
// detected by interface assertion, never reflection. OnConnect runs once when
// the stream opens; it registers the island's timers and subscriptions.
type Live interface {
	OnConnect(*Ctx) error
}

type tickReg struct {
	d  time.Duration
	fn func(*Ctx)
}

// subStarter spawns one subscription's reader goroutine, bridging an external
// channel into the island's single pulse loop. via builds these in Subscribe.
type subStarter func(reqCtx context.Context, pulse chan<- func())

// Tick schedules fn to run every d for the life of the island's connection. fn
// is a named method value (e.g. c.beat); after each run via re-renders the
// island and pushes an element-patch over the SSE stream. Valid only inside
// OnConnect of a live composition.
func (c *Ctx) Tick(d time.Duration, fn func(*Ctx)) {
	c.ticks = append(c.ticks, tickReg{d: d, fn: fn})
}

// OnDispose registers a teardown function run when the island's connection
// closes — stop subscriptions, release producers. fn is a named method value
// (e.g. sub.Stop). Valid only inside OnConnect.
func (c *Ctx) OnDispose(fn func()) { c.disposers = append(c.disposers, fn) }

// Subscribe drives a live island from an external channel: each value runs
// handler on the island's single goroutine (serialized with Tick, so island
// state is mutated race-free) and then via re-renders and pushes. It is a free
// function, not a Ctx method, because Go methods cannot have type parameters;
// the no-'&'/named-method-value ergonomics are unchanged (handler is e.g.
// c.OnMessage). Valid only inside OnConnect; pair it with OnDispose to stop the
// source.
// Listen wires an island to a Topic in one line: it subscribes, pumps every
// published value into handler on the island's own goroutine (then pushes this
// island's re-render), and stops the subscription on disconnect. It fuses the
// Subscribe/OnDispose(sub.Stop)/pump triple — reach for Subscribe only when
// the source is a raw channel rather than a Topic.
func Listen[T any](ctx *Ctx, t *topic.Topic[T], handler func(*Ctx, T)) {
	sub := t.Subscribe()
	ctx.OnDispose(sub.Stop)
	Subscribe(ctx, sub.C(), handler)
}

func Subscribe[T any](ctx *Ctx, ch <-chan T, handler func(*Ctx, T)) {
	ctx.subs = append(ctx.subs, func(reqCtx context.Context, pulse chan<- func()) {
		go func() {
			for {
				select {
				case <-reqCtx.Done():
					return
				case v, ok := <-ch:
					if !ok {
						return
					}
					// The enqueued unit mutates THIS island (ctx) and pushes only
					// THIS island's container — so on a multiplex page a fan-out to
					// one island never re-renders a sibling.
					select {
					case pulse <- func() { handler(ctx, v); ctx.push() }:
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
// DOM by id (default mode). The fragment is emitted as one `data: elements`
// field per physical line: a bare newline in rendered content (e.g. a value
// carrying "\n") would otherwise split the SSE event and truncate the patch.
// The client rejoins the multi-line payload with newlines, reconstructing it.
func writePatchFrame(w io.Writer, fragment []byte) {
	_, _ = io.WriteString(w, "event: datastar-patch-elements\n")
	for _, line := range bytes.Split(fragment, []byte{'\n'}) {
		_, _ = io.WriteString(w, "data: elements ")
		_, _ = w.Write(line)
		_, _ = io.WriteString(w, "\n")
	}
	_, _ = io.WriteString(w, "\n")
}

// defaultHeartbeat is the keepalive cadence when WithSSEHeartbeat is unset or
// non-positive. A failed keepalive write is the only in-band way to detect a
// half-open peer, so the keepalive is never disabled — a non-positive value
// floors to this.
const defaultHeartbeat = 25 * time.Second

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
// goroutine forever; timeout <= 0 disables it. All calls run on the island
// goroutine, so it needs no lock.
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

// runLiveStream drives one or more islands on a single goroutine. Every island's
// ticks, subscriptions, dispatched actions (via liveConn.Dispatch), AND the
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
				d()
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
			fn()
		case <-beat.C:
			keepalive()
		}
	}
}

// liveConn is a connected tab's live island, kept in the per-Register registry
// so a POST action can be routed onto its single goroutine.
type liveConn struct {
	inst        viewer            // legacy single-island instance (carries State[T]); nil for a mux connection
	pulse       chan func()       // the connection's serialization channel (shared by all its islands)
	done        <-chan struct{}   // reqCtx.Done() — closed on disconnect
	push        func()            // legacy: re-render the single island and frame it
	pushSignals func(json string) // emit a patch-signals frame on this stream
	islands     map[int]*Ctx      // mux: islandIdx → its unit Ctx (islandV + push); nil for a legacy connection
}

// Dispatch routes one action unit onto the island goroutine, where it runs
// serialized with ticks/subs/renders. It is select-guarded so a POST racing a
// just-closed tab returns false (the caller answers 410) instead of blocking on
// the unbuffered channel forever.
func (c *liveConn) Dispatch(fn func()) bool {
	select {
	case c.pulse <- fn:
		return true
	case <-c.done:
		return false
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
