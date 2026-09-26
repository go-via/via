package via

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// writePatchFrame writes one Datastar element-patch SSE event; the client
// morphs the fragment into the live DOM by id (default mode).
func writePatchFrame(w io.Writer, fragment []byte) {
	_, _ = io.WriteString(w, "event: datastar-patch-elements\n")
	writeElementLines(w, fragment)
	_, _ = io.WriteString(w, "\n")
}

// writeInnerPatchFrame patches the children of #id, never comparing id's own
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

// stream serializes every write and tears the stream down on the first write
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
	// busy says what the goroutine is waiting on outside user code, for the
	// pinned warning: read from the pin timer's goroutine, hence atomic.
	busy atomic.Int32
	// serverEnded is set when via itself ends the stream (Rotate, a revoked
	// session, Router.Close, an aborted push), as opposed to the client
	// hanging up or a write failing, whose clients retry under the same id.
	serverEnded atomic.Bool
}

// end closes the stream from via's side, from any goroutine.
func (s *stream) end() {
	s.serverEnded.Store(true)
	s.cancel()
}

const (
	busyNone int32 = iota
	busyWrite
	busyStore
)

// blockedOn names what a pinned stream goroutine is stuck in.
func (s *stream) blockedOn() string {
	switch s.busy.Load() {
	case busyWrite:
		return "writing a frame to a client that is not reading"
	case busyStore:
		return "waiting on the session store"
	}
	return "inside a Tick, Listen, OnConnect, OnDispose or action handler"
}

// frame emits one SSE event and flushes it, cancelling the stream on any
// error. Once failed it is a no-op, so a write racing a just-torn-down stream
// can't re-trigger teardown.
func (s *stream) frame(write func(io.Writer)) {
	if s.failed {
		return
	}
	s.busy.Store(busyWrite)
	defer s.busy.Store(busyNone)
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
	s.end()
}

// runStream drives one or more live units on a single goroutine: every unit's
// ticks, subscriptions, dispatched actions and the keepalive feed through it,
// so all mutation, render and stream writes are serialized without a lock. Each
// push item pushes only its own child's container. It always loops, even with
// no ticks or subs, so an interactive-only child still receives actions and
// beats; disposers run on exit.
// listeners and wake are built by the caller, before any OnConnect runs, so a
// unit observes its own connect-time publish; runStream only drains them.
func runStream(log *slog.Logger, reqCtx context.Context, g *pushGuard, children []*Ctx, listeners []listener, wake chan struct{}, pushq chan func(), keepalive func(), interval time.Duration) {
	defer func() {
		for _, child := range children {
			for _, d := range child.disposers {
				runRecovered(log, g, d)
			}
		}
		g.close(log)
	}()
	for _, child := range children {
		for _, t := range child.ticks {
			startTicker(reqCtx, child, t, pushq)
		}
	}
	beat := time.NewTicker(interval)
	defer beat.Stop()
	for {
		select {
		case <-reqCtx.Done():
			return
		case fn := <-pushq:
			runPushItem(log, g, fn)
		case <-wake:
			sweepListeners(log, g, listeners)
		case <-beat.C:
			runPushItem(log, g, keepalive)
		}
	}
}

// sweepListeners drains every listener once, in registration order, so two
// Listens fire deterministically instead of racing two reader goroutines;
// one wake token may stand for any number of pending subscriptions.
func sweepListeners(log *slog.Logger, g *pushGuard, listeners []listener) {
	for _, l := range listeners {
		if work := l.poll(); work != nil {
			runPushItem(log, g, work)
		}
	}
}

// runPushItem recovers per item, so one bad render logs and drops that item
// instead of unwinding runStream: an action's result is already sent to the
// waiting POST by the time pushWork runs, so without this the stream would die
// silently after a 204 the client already saw as success.
//
// A panic does not end the stream: via cannot tell a one-off from one every
// beat repeats, and ending it would trade a unit that stopped updating for a
// reload loop that ends in a "Disconnected." banner over units that still
// work. pushGuard keeps the log to one stack per site.
func runPushItem(log *slog.Logger, g *pushGuard, fn func()) {
	g.arm()
	defer g.disarm()
	runRecovered(log, g, fn)
}

// runRecovered is runPushItem without the pin timer, for OnDispose: the tab is
// closing, so no action is left to answer 503 and a slow teardown is not a
// pinned tab. Still recovered, since a disposer is user code and one panicking
// must not skip the rest and leak what they release.
func runRecovered(log *slog.Logger, g *pushGuard, fn func()) {
	defer func() {
		if rec := recover(); rec != nil {
			g.panicked(log, rec)
		}
	}()
	fn()
}

// panicSummaryEvery bounds how often a panic that repeats at one site is
// reported after its first stack, so a Tick that panics every beat does not
// log a stack per beat.
const panicSummaryEvery = time.Minute

// pushGuard is one stream's watch over its own push items. It carries the
// connection's identity into every log line, keeps a panic that repeats at one
// site to its first stack plus a periodic count, and reports an item that runs
// past the pinned deadline even when no action is waiting to notice. All but
// the pin timer's callback run on the stream goroutine.
type pushGuard struct {
	label  string
	panics map[string]*panicTally
	pin    *time.Timer
	pinFor time.Duration
}

type panicTally struct {
	repeats int
	since   time.Time
	last    any
}

func newPushGuard(label string) *pushGuard { return &pushGuard{label: label} }

// watchPin makes an item still running after d call pinned, from the timer's
// own goroutine, since the stream goroutine is the one that is stuck.
func (g *pushGuard) watchPin(d time.Duration, pinned func()) {
	g.pinFor = d
	g.pin = time.AfterFunc(d, pinned)
	g.pin.Stop()
}

func (g *pushGuard) arm() {
	if g.pin != nil {
		g.pin.Reset(g.pinFor)
	}
}

func (g *pushGuard) disarm() {
	if g.pin != nil {
		g.pin.Stop()
	}
}

func (g *pushGuard) panicked(log *slog.Logger, rec any) {
	site := panicSite()
	t := g.panics[site]
	if t == nil {
		if g.panics == nil {
			g.panics = map[string]*panicTally{}
		}
		g.panics[site] = &panicTally{since: time.Now()}
		log.Error("via: live push panic", "label", g.label, "err", rec, "stack", string(debug.Stack()))
		return
	}
	t.repeats++
	t.last = rec
	if time.Since(t.since) >= panicSummaryEvery {
		g.report(log, site, t)
	}
}

func (g *pushGuard) report(log *slog.Logger, site string, t *panicTally) {
	log.Error("via: live push panic repeated", "label", g.label, "site", site, "count", t.repeats,
		"over", time.Since(t.since).Round(time.Second), "err", t.last)
	t.repeats, t.since = 0, time.Now()
}

// close reports what repeated since the last summary, so a stream that ends
// between two summaries still accounts for every panic.
func (g *pushGuard) close(log *slog.Logger) {
	g.disarm()
	for site, t := range g.panics {
		if t.repeats > 0 {
			g.report(log, site, t)
		}
	}
}

// panicSite is the file:line the recovered panic started at. A deferred
// recover that re-panics (rootPush's) sits between, so it is the frame past
// the deepest runtime.gopanic, not the nearest.
func panicSite() string {
	pcs := make([]uintptr, 64)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
	site, past := "unknown", false
	for {
		f, more := frames.Next()
		switch {
		case f.Function == "runtime.gopanic":
			past = true
		case past && !strings.HasPrefix(f.Function, "runtime."):
			site, past = f.File+":"+strconv.Itoa(f.Line), false
		}
		if !more {
			return site
		}
	}
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
// and the browser from buffering frames. A session's stricter no-store (see
// noStore) is kept; anything else, such as a middleware's public max-age, is
// replaced.
func writeSSEHeaders(w http.ResponseWriter) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	if hdr.Get("Cache-Control") != noStoreValue {
		hdr.Set("Cache-Control", "no-cache")
	}
	hdr.Set("X-Content-Type-Options", "nosniff")
}

// canFlush reports whether w can stream, looking through wrappers the way
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
