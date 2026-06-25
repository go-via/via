package via

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"time"
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
type subStarter func(reqCtx context.Context, pulse chan<- func(*Ctx))

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
func Subscribe[T any](ctx *Ctx, ch <-chan T, handler func(*Ctx, T)) {
	ctx.subs = append(ctx.subs, func(reqCtx context.Context, pulse chan<- func(*Ctx)) {
		go func() {
			for {
				select {
				case <-reqCtx.Done():
					return
				case v, ok := <-ch:
					if !ok {
						return
					}
					select {
					case pulse <- func(ic *Ctx) { handler(ic, v) }:
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

// runLiveStream drives the island on a single goroutine. Ticks, subscriptions,
// AND dispatched actions (from the POST handler via liveConn.Dispatch) all feed
// the one pulse channel — created by the caller and shared with the registry —
// so every island mutation + render is serialized, no lock. It always loops
// (even with no ticks/subs) so an interactive-only island still receives
// dispatched actions; disposers run on exit (client disconnect).
func runLiveStream(reqCtx context.Context, island *Ctx, pulse chan func(*Ctx), push func()) {
	defer func() {
		for _, d := range island.disposers {
			d()
		}
	}()
	for _, t := range island.ticks {
		startTicker(reqCtx, t, pulse)
	}
	for _, start := range island.subs {
		start(reqCtx, pulse)
	}
	for {
		select {
		case <-reqCtx.Done():
			return
		case fn := <-pulse:
			fn(island)
			push()
		}
	}
}

// liveConn is a connected tab's live island, kept in the per-Register registry
// so a POST action can be routed onto its single goroutine.
type liveConn struct {
	inst        viewer            // this connection's island instance (carries State[T])
	pulse       chan func(*Ctx)   // the island's serialization channel
	done        <-chan struct{}   // reqCtx.Done() — closed on disconnect
	pushSignals func(json string) // emit a patch-signals frame on this stream
}

// Dispatch routes one action fn onto the island goroutine, where it runs
// serialized with ticks/subs/renders. It is select-guarded so a POST racing a
// just-closed tab returns false (the caller answers 410) instead of blocking on
// the unbuffered channel forever.
func (c *liveConn) Dispatch(fn func(*Ctx)) bool {
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

func startTicker(reqCtx context.Context, t tickReg, pulse chan<- func(*Ctx)) {
	go func() {
		tk := time.NewTicker(t.d)
		defer tk.Stop()
		for {
			select {
			case <-reqCtx.Done():
				return
			case <-tk.C:
				select {
				case pulse <- t.fn:
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
