package via

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"time"
)

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
// is how a live child's push still lands on a container the root-walk render
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
// gone; cancelling stops the child goroutine, its tickers and subscriptions and
// runs disposers instead of leaking them against a dead socket. The per-frame
// deadline keeps a stalled-but-alive peer from pinning the goroutine forever.
// All calls run on the child goroutine, so it needs no lock.
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
	_ = s.rc.SetWriteDeadline(time.Now().Add(s.timeout))
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
// push item pushes only its own child's container. It always loops, even with
// no ticks or subs, so an interactive-only child still receives actions and
// beats; disposers run on exit.
// listeners and wake are built by the caller, before any OnConnect runs, so a
// unit observes its own connect-time publish; runStream only drains them.
func runStream(reqCtx context.Context, label string, children []*Ctx, listeners []listener, wake chan struct{}, pushq chan func(), keepalive func(), interval time.Duration) {
	defer func() {
		for _, child := range children {
			for _, d := range child.disposers {
				// runPushItem, not a bare call: a disposer is user code, and one
				// panicking must not skip the rest and leak what they release.
				runPushItem(label, d)
			}
		}
	}()
	for _, child := range children {
		for _, t := range child.ticks {
			startTicker(reqCtx, child, t, pushq)
		}
	}
	// One token may stand for any number of subscriptions, so every wake sweeps
	// them ALL, in registration order — which makes handler ordering across two
	// Listens deterministic instead of a race between two reader goroutines.
	sweep := func() {
		for _, l := range listeners {
			if work := l.poll(); work != nil {
				runPushItem(label, work)
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
			runPushItem(label, fn)
		case <-wake:
			sweep()
		case <-beat.C:
			runPushItem(label, keepalive)
		}
	}
}

// runPushItem recovers per item, so one bad render logs and drops that item
// instead of unwinding runStream: an action's result is already sent to the
// waiting POST by the time pushWork runs, so without this the stream would die
// silently after a 204 the client already saw as success. label carries the
// connection's identity, so a busy deploy's stacks group by tab and unit
// instead of being read one by one.
func runPushItem(label string, fn func()) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("via: live push panic%s: %v\n%s", label, rec, debug.Stack())
		}
	}()
	fn()
}

// writeSignalsFrame writes one Datastar patch-signals SSE event — via uses it
// to hand the client its tab id, whose name must stay underscore-free (see
// tabSignal).
func writeSignalsFrame(w io.Writer, signalsJSON string) {
	_, _ = io.WriteString(w, "event: datastar-patch-signals\ndata: signals ")
	_, _ = io.WriteString(w, signalsJSON)
	_, _ = io.WriteString(w, "\n\n")
}

// writeSSEHeaders sets the event-stream headers; Cache-Control keeps proxies
// and the browser from buffering frames.
func writeSSEHeaders(w http.ResponseWriter) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("X-Content-Type-Options", "nosniff")
}

// canFlush reports whether w can stream, looking THROUGH wrappers the way
// http.ResponseController does. A raw w.(http.Flusher) is wrong here — any
// middleware wrapper answers no — and ResponseController.Flush cannot serve as
// the probe either, because flushing commits a 200 ahead of the checks that
// follow it.
func canFlush(w http.ResponseWriter) bool {
	for {
		switch v := w.(type) {
		case interface{ FlushError() error }:
			return true
		case http.Flusher:
			return true
		case interface{ Unwrap() http.ResponseWriter }:
			w = v.Unwrap()
		default:
			return false
		}
	}
}
