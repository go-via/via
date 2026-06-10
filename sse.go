package via

import (
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/starfederation/datastar-go/datastar"
)

// sseLevel is the brotli compression level applied to SSE streams.
// Level 5 trades a bit of CPU for noticeable bandwidth savings on the
// repetitive HTML element patches via emits.
const sseLevel = 5

// keepaliveFloor is the minimum SSE keepalive cadence. WithSSEHeartbeat(0)
// floors to this rather than disabling, because under the connection-presence
// liveness model a failed keepalive write is the only in-band detector of a
// vanished (half-open) client — the TTL sweep can't reap a connected ctx.
const keepaliveFloor = 25 * time.Second

// heartbeatPayload is the empty-signals JSON object sent on every SSE
// keepalive tick. Cached so we don't allocate two bytes per tick per
// live tab (datastar treats the slice as immutable once handed off).
var heartbeatPayload = []byte("{}")

// handleSSE opens the persistent stream for a Ctx identified by the via_tab
// signal sent in the URL, drains the patch queue until the client goes away
// or the ctx is disposed.
func (a *App) handleSSE(w http.ResponseWriter, r *http.Request) {
	var sigs map[string]any
	_ = datastar.ReadSignals(r, &sigs)
	tabID, _ := sigs[tabSignalKey].(string)

	ctx, ok := a.getCtx(tabID)
	if !ok {
		// Stale-but-plausible tab id (TTL sweep, process restart):
		// re-bootstrap a fresh Ctx over this same stream instead of
		// 404ing into Datastar's infinite retry (a frozen tab).
		a.recoverSSE(w, r, tabID)
		return
	}
	if sess := ctx.session.Load(); sess != nil && a.sessionFromRequest(r) != sess {
		a.metricsOrNoop().Counter("via.session.mismatch")
		a.logErr(ctx, "session mismatch on SSE handshake: the tab's bound session no longer matches the request cookie (two via apps on the same host:port clobbering via_session?) — the tab will freeze on Datastar retry exhaustion")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	ctx.touch()

	// Same posture as the page render and action POST: run the
	// descriptor's group middleware so a requireAuth-style guard can
	// veto the SSE handshake before the stream goes hot.
	stream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runSSEStream(a, ctx, w, r, nil)
	})
	applyMiddleware(ctx.desc.groupMW, stream).ServeHTTP(w, requestWithRoute(r, ctx.desc.route))
}

func runSSEStream(a *App, ctx *Ctx, w http.ResponseWriter, r *http.Request, boot *sseBootstrap) {
	// nginx (and similar reverse proxies) buffer proxied responses by default,
	// which holds SSE frames until the buffer fills — heartbeat and patches
	// never reach the browser. Opt the stream out before NewSSE writes headers.
	// datastar's NewSSE already sets Cache-Control/Content-Type/Connection.
	w.Header().Set("X-Accel-Buffering", "no")
	m := a.metricsOrNoop()
	m.Counter("via.sse.connect")
	// Default to "client": every exit path other than a server-side
	// disposal (shutdown / TTL sweep) is the client going away — its
	// request context cancelling or a heartbeat/patch write failing. The
	// doneChan path overrides this with the reason recorded on disposal.
	reason := disconnectClient
	defer func() { m.Counter("via.sse.disconnect", "reason", reason) }()
	// An open stream is itself proof the tab is alive: keep this ctx out of
	// the TTL sweep for the connection's lifetime (removeExpiredContexts
	// skips connected>0). On exit the counter drops back to zero and a
	// stream-less ctx is reaped by the next sweep once it ages past the TTL.
	ctx.connected.Add(1)
	defer ctx.connected.Add(-1)
	// OnConnect runs once, the first time the SSE stream is opened. Bots
	// that hit GET without ever opening the SSE never see this fire, so
	// expensive background work (tickers, fan-out goroutines) lives here
	// rather than in OnInit.
	ctx.connectOnce.Do(func() {
		if ctx.connectFn == nil {
			return
		}
		defer recoverLog(ctx, "OnConnect")
		if err := ctx.connectFn(ctx); err != nil {
			a.logErr(ctx, "OnConnect: %v", err)
		}
	})

	sse := datastar.NewSSE(w, r,
		datastar.WithCompression(datastar.WithBrotli(datastar.WithBrotliLevel(sseLevel))))

	// Latch-and-branch on connection history. A re-bootstrap (boot != nil)
	// seeds the fresh tab wholesale: signals first (incl. the new via_tab,
	// so data-* bindings in the incoming elements resolve), then the full
	// view replacing the stale container. A plain reconnect re-ships only
	// the view — never signals, which would clobber live client-side
	// signal state — so a client that drifted while disconnected (e.g. a
	// trimmed queue, a missed frame) converges back to server truth.
	if reconnect := ctx.everConnected.Swap(true); boot != nil {
		setSSEWriteDeadline(w, a.cfg.sseWriteTimeout)
		if err := sse.PatchSignals(boot.signals); err != nil {
			return
		}
		if boot.elements != "" {
			setSSEWriteDeadline(w, a.cfg.sseWriteTimeout)
			if err := sse.PatchElements(boot.elements,
				datastar.WithSelector(boot.selector),
				datastar.WithMode(datastar.ElementPatchModeReplace)); err != nil {
				return
			}
		}
	} else if reconnect {
		m.Counter("via.sse.resync")
		if frag := a.renderFragment(ctx); frag != "" {
			setSSEWriteDeadline(w, a.cfg.sseWriteTimeout)
			if err := sse.PatchElements(frag); err != nil {
				return
			}
		}
	}

	// Force-drain anything queued while the previous SSE was
	// disconnected — patches accumulated during the gap have no wake
	// notification waiting (it was either consumed by the dead loop or
	// never sent if the previous drain was mid-flight). Without this,
	// the reconnected client sees stale UI until the next notify.
	if hasPending(ctx.queue) {
		if err := drainQueue(sse, ctx, w, a.cfg.sseWriteTimeout); err != nil {
			return
		}
	}

	// Emit an SSE comment line so the client (and tests) can observe
	// that the SSE goroutine has entered its select loop and is
	// registered to receive patch-queue wakeups. Comments start with
	// `:` per the SSE spec — Datastar (and any conformant client)
	// silently ignores them, so this adds no event surface. Tests use
	// it to replace the timing-based 20ms-sleep idiom.
	//
	// Only safe when the stream is NOT content-encoded: datastar routes
	// its own writes through a compressing writer, but this raw write
	// goes to the underlying ResponseWriter — uncompressed bytes in a
	// Content-Encoding: br stream corrupt it for a real browser. The
	// marker is an observability/test aid clients ignore, and the test
	// client negotiates no encoding, so it still observes it.
	if w.Header().Get("Content-Encoding") == "" {
		setSSEWriteDeadline(w, a.cfg.sseWriteTimeout)
		if _, err := io.WriteString(w, ": ready\n\n"); err != nil {
			return
		}
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}

	// The keepalive is always on. Under the connection-presence liveness
	// model the sweep can't reap a connected ctx, so a failed keepalive
	// write is the only in-band way to detect a vanished (half-open) peer;
	// WithSSEHeartbeat(0) floors the cadence rather than disabling it.
	keepalive := a.cfg.sseHeartbeat
	if keepalive <= 0 {
		keepalive = keepaliveFloor
	}
	t := time.NewTicker(keepalive)
	defer t.Stop()

	for {
		select {
		case <-sse.Context().Done():
			return
		case <-ctx.doneChan:
			reason = ctx.disposeReasonOrDefault(disconnectClient)
			return
		case <-t.C:
			// Keepalive: a real write that fails on a dead peer (the ctx's
			// own liveness is owned by connected, not lastAccess). A
			// successful tick also proves the tab is alive, so keep its
			// session warm — the session sweep keys on lastAccess and would
			// otherwise reap a live-but-idle stream's session out from under it.
			setSSEWriteDeadline(w, a.cfg.sseWriteTimeout)
			if err := sse.PatchSignals(heartbeatPayload); err != nil {
				return
			}
			ctx.touchSession()
		case <-ctx.queue.wake:
			if err := drainQueue(sse, ctx, w, a.cfg.sseWriteTimeout); err != nil {
				return
			}
			ctx.touch()
		}
	}
}

// setSSEWriteDeadline installs a per-call write deadline so a stalled
// peer can't pin the SSE goroutine forever. Wrapped to swallow the
// "not supported" case the response writer may surface when the runtime
// doesn't expose deadline control (rare, but possible behind some
// reverse-proxy middlewares).
func setSSEWriteDeadline(w http.ResponseWriter, d time.Duration) {
	if d <= 0 {
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d))
}

// hasPending reports whether the patch queue holds anything to flush.
// Cheap snapshot under the lock — used by the SSE handshake to drain
// a backlog from the previous (dropped) connection without waiting for
// the next notify.
func hasPending(q *patchQueue) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.autoElements != "" || q.elements != "" || q.redirect != "" ||
		len(q.signals) > 0 || q.scripts.Len() > 0
}

func drainQueue(sse *datastar.ServerSentEventGenerator, ctx *Ctx, w http.ResponseWriter, writeTimeout time.Duration) error {
	setSSEWriteDeadline(w, writeTimeout)
	q := ctx.queue
	q.mu.Lock()
	// Auto render first, explicit patches after: the morph applies
	// same-id patches last-wins, so the user's targeted override beats
	// the auto render of the same element.
	elems := q.autoElements + q.elements
	signals := q.signals
	scripts := q.scripts.String()
	redirect := q.redirect
	q.autoElements = ""
	q.elements = ""
	q.signals = nil
	q.scripts.Reset()
	q.redirect = ""
	q.mu.Unlock()

	// Re-arm the write deadline before EACH network write: a single deadline
	// set at entry would span the sum of up to four sequential writes, so a
	// peer that stalls on a later write has already burned the budget on the
	// earlier ones. Per-write keeps every write bounded independently.
	nonceOpts := ctx.scriptNonceOpts()
	if redirect != "" {
		setSSEWriteDeadline(w, writeTimeout)
		return sse.Redirect(redirect, nonceOpts...)
	}
	if elems != "" {
		setSSEWriteDeadline(w, writeTimeout)
		if err := sse.PatchElements(elems); err != nil {
			return err
		}
	}
	if len(signals) > 0 {
		out, err := json.Marshal(signals)
		if err != nil {
			// User pushed an unmarshalable value via PatchSignal(s) /
			// BroadcastSignals (e.g. a channel or func in the map). Log
			// and skip the signal frame rather than emit malformed JSON.
			if ctx.app != nil {
				ctx.app.logErr(ctx, "drainQueue: json.Marshal signals: %v", err)
			}
		} else {
			setSSEWriteDeadline(w, writeTimeout)
			if err := sse.PatchSignals(out); err != nil {
				recycleSignalsMap(q, signals)
				return err
			}
		}
		recycleSignalsMap(q, signals)
	}
	if scripts != "" {
		setSSEWriteDeadline(w, writeTimeout)
		if err := sse.ExecuteScript(scripts, nonceOpts...); err != nil {
			return err
		}
	}
	return nil
}

// scriptNonceOpts threads the page document's captured CSP nonce onto the
// <script> elements datastar injects for ExecuteScript / Redirect, so they
// survive a strict `script-src 'nonce-…'` policy. Returns nil when no nonce
// was captured (no CSP middleware), keeping the push attribute-free. The
// value is HTML-escaped at this sink — mirroring the document render path
// (the h builder escapes attributes) — so a non-base64 nonce threaded via
// the exported RequestWithCSPNonce can't break out of the attribute.
func (ctx *Ctx) scriptNonceOpts() []datastar.ExecuteScriptOption {
	n := ctx.documentCSPNonce()
	if n == "" {
		return nil
	}
	return []datastar.ExecuteScriptOption{
		datastar.WithExecuteScriptAttributes(`nonce="` + html.EscapeString(n) + `"`),
	}
}

// recycleSignalsMap returns m to the queue for reuse if no producer
// has installed a fresh map between drain and now. Clearing-and-
// recycling avoids reallocating the map on every flush in
// signal-heavy real-time flows. Cap at a modest size so a single
// outlier flush doesn't pin a huge map alive.
func recycleSignalsMap(q *patchQueue, m map[string]any) {
	if len(m) > 256 {
		return
	}
	clear(m)
	q.mu.Lock()
	if q.signals == nil {
		q.signals = m
	}
	q.mu.Unlock()
}

func (a *App) handleSSEClose(w http.ResponseWriter, r *http.Request) {
	maxBody := a.cfg.maxRequestBody
	if maxBody == 0 {
		maxBody = 4096
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	tabID := strings.TrimSpace(string(body))
	if ctx, ok := a.getCtx(tabID); ok {
		if sess := ctx.session.Load(); sess != nil && a.sessionFromRequest(r) != sess {
			return
		}
		// Unregister first so concurrent action handlers see "not
		// found" and 404 instead of finding a half-disposed Ctx that
		// they then try to operate on. disposeCtx is idempotent so
		// the dispose-after-unregister order is safe.
		a.unregisterCtx(tabID)
		a.disposeCtx(ctx, disconnectClient)
	}
}
