package via

import (
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"

	"github.com/starfederation/datastar-go/datastar"
)

// This file implements SSE reconnect recovery: when a stream reconnects
// with a via_tab the process no longer knows (TTL sweep, deploy/restart),
// the old behavior was a 404 — which Datastar retries forever, freezing
// the tab. Instead, a stale-but-plausible id re-bootstraps a fresh Ctx
// over the same stream: the route is recovered from the tab id's prefix,
// path/query params from the Referer, then OnInit runs and the full view
// plus a fresh signal seed (including a new via_tab) replace the stale
// state client-side. When the page request can't be reconstructed (a
// param route with no usable Referer, or the app is at capacity), the
// stream degrades to an explicit window.location.reload() — predictable
// recovery, never a silent freeze.

// sseBootstrap is the recovery payload runSSEStream ships before entering
// its drain loop: the fresh signal seed and the full-view fragment that
// replaces the stale tab container.
type sseBootstrap struct {
	signals  []byte // fresh seed: via_tab + app signals + Signal[T] slots
	elements string // full view wrapped in the new ctx's container div
	selector string // CSS selector addressing the stale container
}

// staleTabSuffixRE pins the random component of a recoverable tab id to
// exactly what genSecureID emits (64 hex chars). Anything else is a forged
// or garbage id and keeps the historical 404.
var staleTabSuffixRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// descriptorForStaleTab maps a stale tab id back to its mounted descriptor
// via the route prefix genTabID baked in (`<route>_<64-hex>`). nil when the
// id is malformed or names a route this process never mounted.
func (a *App) descriptorForStaleTab(tabID string) *cmpDescriptor {
	i := strings.LastIndexByte(tabID, '_')
	if i <= 0 || !staleTabSuffixRE.MatchString(tabID[i+1:]) {
		return nil
	}
	route := tabID[:i]
	a.descsMu.RLock()
	defer a.descsMu.RUnlock()
	for _, d := range a.descs {
		if d.route == route {
			return d
		}
	}
	return nil
}

// recoverSSE handles an SSE handshake whose via_tab is unknown. Runs the
// descriptor's group middleware first — same posture as the page render
// and the known-tab handshake — so a requireAuth-style guard vetoes the
// re-bootstrap exactly as it would veto the page.
func (a *App) recoverSSE(w http.ResponseWriter, r *http.Request, staleID string) {
	d := a.descriptorForStaleTab(staleID)
	if d == nil {
		// Forged / garbage id: keep the historical 404 so junk traffic
		// can't mint contexts.
		w.WriteHeader(http.StatusNotFound)
		return
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := a.metricsOrNoop()
		pageReq := a.recoveredPageRequest(r, d)
		if pageReq == nil {
			m.Counter("via.sse.recover", "mode", "reload")
			a.streamReloadScript(w, r)
			return
		}
		ctx, boot := a.rebootstrapCtx(d, w, r, pageReq, staleID)
		if ctx == nil {
			m.Counter("via.sse.recover", "mode", "reload")
			a.streamReloadScript(w, r)
			return
		}
		m.Counter("via.sse.recover", "mode", "rebootstrap")
		runSSEStream(a, ctx, w, r, boot)
	})
	applyMiddleware(d.groupMW, handler).ServeHTTP(w, r)
}

// recoveredPageRequest reconstructs the page GET the original render was
// born from, so path/query params decode into the fresh composition. The
// page URL comes from the SSE request's Referer (same-origin fetches send
// it); a paramless route falls back to its own literal pattern when no
// Referer is available. Returns nil when the request can't be rebuilt —
// the caller degrades to a reload.
func (a *App) recoveredPageRequest(r *http.Request, d *cmpDescriptor) *http.Request {
	var pagePath string
	if ref := r.Referer(); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Path != "" {
			pagePath = u.Path
			if u.RawQuery != "" {
				pagePath += "?" + u.RawQuery
			}
		}
	}
	if pagePath == "" {
		if strings.ContainsRune(d.route, '{') {
			return nil // param values are unrecoverable without a Referer
		}
		pagePath = d.route
	}

	// Route the synthetic GET through a throwaway mux holding only this
	// descriptor's pattern: ServeMux is the sole owner of pattern-matching
	// semantics (and of populating PathValue), so reuse it rather than
	// reimplementing wildcard matching. No match (a Referer from some
	// other page) leaves matched nil → reload fallback.
	synth, err := http.NewRequestWithContext(r.Context(), http.MethodGet, pagePath, nil)
	if err != nil {
		return nil
	}
	var matched *http.Request
	mm := http.NewServeMux()
	mm.HandleFunc("GET "+d.route, func(_ http.ResponseWriter, mr *http.Request) {
		matched = mr
	})
	mm.ServeHTTP(noopResponseWriter{}, synth)
	return matched
}

// rebootstrapCtx mirrors renderPage — fresh *C, params, OnInit, registry
// insert — but renders for the SSE wire instead of a page document. The
// fresh tab id is minted server-side and only ever travels down this
// stream: accepting the client-supplied stale id as the new identity
// would let a cross-origin GET register an attacker-known via_tab against
// the victim's session, breaking the via_tab-as-CSRF-token model.
func (a *App) rebootstrapCtx(d *cmpDescriptor, w http.ResponseWriter, r, pageReq *http.Request, staleID string) (*Ctx, *sseBootstrap) {
	cmpVal := reflect.New(d.typ)
	ctx := newCtx(d, cmpVal, genTabID(d.route))
	ctx.app = a
	ctx.session.Store(a.sessionFromRequest(r))
	ctx.mu.Lock()
	ctx.w = w
	ctx.r = r
	ctx.mu.Unlock()
	ctx.captureCSPNonce(r)
	// Same scoping as renderPage: writer/request live only for the
	// synchronous bootstrap; goroutines launched from OnInit must not
	// hold a dangling reference.
	defer func() {
		ctx.mu.Lock()
		ctx.w = nil
		ctx.r = nil
		ctx.mu.Unlock()
	}()

	decodePathParams(cmpVal, pageReq, d)
	decodeQueryParams(cmpVal, pageReq, d)

	if ctx.initFn != nil {
		func() {
			defer recoverLog(ctx, "OnInit")
			if err := ctx.initFn(ctx); err != nil {
				a.logErr(ctx, "OnInit: %v", err)
			}
		}()
	}

	if !a.tryRegisterCtx(ctx, a.cfg.maxContexts) {
		a.logWarn(nil, "max contexts reached (%d); rejecting SSE re-bootstrap", a.cfg.maxContexts)
		return nil, nil
	}

	sigs, err := json.Marshal(a.initialSignals(ctx))
	if err != nil {
		// Same failure class as writePageDocument: a plugin app signal or
		// Signal[T] init value that can't round-trip. Seed at least the
		// fresh via_tab so actions recover even if the rest didn't encode.
		a.logErr(ctx, "rebootstrapCtx: json.Marshal initial signals: %v", err)
		sigs, _ = json.Marshal(map[string]string{tabSignalKey: ctx.id})
	}

	// The page document's beforeunload beacon closes over the OLD tab id
	// (a no-op close after recovery), so the recovered ctx would only ever
	// be reclaimed by the TTL sweep. Queue a replacement beacon for the
	// fresh id; drainQueue ships it right after the bootstrap frames.
	enqueueScript(ctx, "window.addEventListener('beforeunload',()=>{navigator.sendBeacon('/_sse/close','"+
		template.JSEscapeString(ctx.id)+"');})")

	return ctx, &sseBootstrap{
		signals:  sigs,
		elements: a.renderFragment(ctx),
		selector: staleContainerSelector(staleID),
	}
}

// staleContainerSelector addresses the old tab's container div. An
// attribute selector (not #id) because routes — and therefore tab ids —
// routinely contain `/`, which is invalid unescaped in an id selector.
// The hex suffix is already validated; the route part is server-defined,
// but escape quote/backslash anyway so the selector can't be broken out of.
func staleContainerSelector(staleID string) string {
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(staleID)
	return `[id="` + esc + `"]`
}

// streamReloadScript is the degraded recovery path: open the SSE stream
// (a 200, so Datastar stops hammering the endpoint) and push an explicit
// reload. The subsequent page GET re-bootstraps everything from scratch.
func (a *App) streamReloadScript(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r,
		datastar.WithCompression(datastar.WithBrotli(datastar.WithBrotliLevel(sseLevel))))
	setSSEWriteDeadline(w, a.cfg.sseWriteTimeout)
	var opts []datastar.ExecuteScriptOption
	// No ctx on this path — thread the request's strict-CSP nonce (if a
	// CSP middleware installed one) straight onto the injected <script>.
	if n, ok := r.Context().Value(cspNonceKey{}).(string); ok && n != "" {
		opts = append(opts, datastar.WithExecuteScriptAttributes(`nonce="`+html.EscapeString(n)+`"`))
	}
	_ = sse.ExecuteScript("window.location.reload()", opts...)
}

// noopResponseWriter absorbs the throwaway mux's output (the 404 it writes
// on a non-matching synthetic request).
type noopResponseWriter struct{}

func (noopResponseWriter) Header() http.Header         { return http.Header{} }
func (noopResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (noopResponseWriter) WriteHeader(int)             {}
