package via

import (
	"encoding/json"
	"errors"
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

// heartbeatPayload is the empty-signals JSON object sent on every SSE
// heartbeat tick. Cached so we don't allocate two bytes per tick per
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
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if ctx.session != nil && a.sessionFromRequest(r) != ctx.session {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	ctx.touch()

	// Same posture as the page render and action POST: run the
	// descriptor's group middleware so a requireAuth-style guard can
	// veto the SSE handshake before the stream goes hot.
	stream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runSSEStream(a, ctx, w, r)
	})
	applyMiddleware(ctx.desc.groupMW, stream).ServeHTTP(w, r)
}

func runSSEStream(a *App, ctx *Ctx, w http.ResponseWriter, r *http.Request) {
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

	// Force-drain anything queued while the previous SSE was
	// disconnected — patches accumulated during the gap have no wake
	// notification waiting (it was either consumed by the dead loop or
	// never sent if the previous drain was mid-flight). Without this,
	// the reconnected client sees stale UI until the next notify.
	if hasPending(ctx.queue) {
		if err := drainQueue(sse, ctx); err != nil {
			return
		}
	}

	var heartbeat <-chan time.Time
	if a.cfg.sseHeartbeat > 0 {
		t := time.NewTicker(a.cfg.sseHeartbeat)
		defer t.Stop()
		heartbeat = t.C
	}

	for {
		select {
		case <-sse.Context().Done():
			return
		case <-ctx.doneChan:
			return
		case <-heartbeat:
			if err := sse.PatchSignals(heartbeatPayload); err != nil {
				return
			}
			ctx.touch()
		case <-ctx.queue.wake:
			if err := drainQueue(sse, ctx); err != nil {
				return
			}
			ctx.touch()
		}
	}
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
	return q.elements != "" || q.redirect != "" ||
		len(q.signals) > 0 || q.scripts.Len() > 0
}

func drainQueue(sse *datastar.ServerSentEventGenerator, ctx *Ctx) error {
	q := ctx.queue
	q.mu.Lock()
	elems := q.elements
	signals := q.signals
	scripts := q.scripts.String()
	redirect := q.redirect
	q.elements = ""
	q.signals = nil
	q.scripts.Reset()
	q.redirect = ""
	q.mu.Unlock()

	if redirect != "" {
		return sse.Redirect(redirect)
	}
	if elems != "" {
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
		} else if err := sse.PatchSignals(out); err != nil {
			recycleSignalsMap(q, signals)
			return err
		}
		recycleSignalsMap(q, signals)
	}
	if scripts != "" {
		if err := sse.ExecuteScript(scripts); err != nil {
			return err
		}
	}
	return nil
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
		if ctx.session != nil && a.sessionFromRequest(r) != ctx.session {
			return
		}
		// Unregister first so concurrent action handlers see "not
		// found" and 404 instead of finding a half-disposed Ctx that
		// they then try to operate on. disposeCtx is idempotent so
		// the dispose-after-unregister order is safe.
		a.unregisterCtx(tabID)
		a.disposeCtx(ctx)
	}
}
