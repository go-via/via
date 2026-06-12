// Package mw provides the recommended HTTP middleware stack for via
// apps: request-id stamping, access logs, panic recovery, strict CSP,
// HSTS, and plain-HTTP → HTTPS redirects.
//
//	app := via.New()
//	mw.Defaults(app)              // RequestID + AccessLog + Recover
//	app.Use(mw.CSP())             // strict CSP with per-request nonce
//	app.Use(mw.HSTS())            // HSTS for HTTPS deployments
//	app.Use(mw.RedirectHTTPS())   // 301 plain HTTP → https
//
// Logs flow through the App's configured [via.Logger] — install one
// with [via.WithLogger] (default: log.Printf).
package mw

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-via/via"
)

// Defaults installs the recommended middleware stack on a: RequestID
// (X-Request-ID stamping), AccessLog (one info line per request with
// the captured status + rid), Recover (panic → 500). Order matters:
// RequestID is outermost so AccessLog can read the id from r.Context;
// AccessLog wraps Recover so it sees the final status (500 after
// Recover writes) on the deferred log line.
//
//	app := via.New()
//	mw.Defaults(app)
//	via.Mount[Counter](app, "/")
func Defaults(a *via.App) {
	a.Use(RequestID(), AccessLog(a), Recover(a))
}

// RequestID returns a [via.Middleware] that ensures every request
// carries an X-Request-ID — using the inbound header value if
// present, otherwise generating a fresh 16-byte base64url id. The id
// is mirrored back on the response so clients can quote it when
// reporting issues.
//
//	app.Use(mw.RequestID())
//	app.Use(mw.AccessLog(app))   // sees the same id in subsequent logs
//
// The id is also planted on r.Context; [via.Log] includes it in the
// kv pairs when present.
func RequestID() via.Middleware {
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = randID() // 16-byte base64url
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, via.RequestWithID(r, id))
	}
}

// RequestIDFrom is a re-export of [via.RequestIDFrom] for callers
// already importing mw. Returns "" if no RequestID middleware has run.
func RequestIDFrom(r *http.Request) string { return via.RequestIDFrom(r) }

// AccessLog returns a [via.Middleware] that emits one info-level log
// record per HTTP request through a's configured logger:
//
//	app.Use(mw.AccessLog(app))
//
// Format: method=GET path=/foo status=200 duration=1.2ms rid=…
// Status is captured by wrapping the ResponseWriter; default 200 if
// the handler never calls WriteHeader.
func AccessLog(a *via.App) via.Middleware {
	logger := a.Logger()
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		dur := time.Since(start)
		method, path := sanitizeLog(r.Method), sanitizeLog(r.URL.Path)
		if rid := via.RequestIDFrom(r); rid != "" {
			logger.Log(via.LogInfo,
				method+" "+path+" status="+strconv.Itoa(sw.status)+
					" duration="+dur.String()+" rid="+sanitizeLog(rid))
		} else {
			logger.Log(via.LogInfo,
				method+" "+path+" status="+strconv.Itoa(sw.status)+
					" duration="+dur.String())
		}
	}
}

// Recover returns a [via.Middleware] that catches panics in
// downstream handlers, logs the recovered value through a's logger,
// and writes a 500 response so the goroutine doesn't crash the
// server.
//
// Action handlers already have per-action panic recovery (so action
// panics surface through [via.WithActionErrorHandler] / the default
// alert). Recover protects everything else — non-via handlers via
// HandleFunc, custom middleware, plugin endpoints — that wouldn't
// otherwise have a backstop:
//
//	app.Use(mw.Recover(app))
func Recover(a *via.App) via.Middleware {
	logger := a.Logger()
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Log(via.LogError,
					"panic in handler "+sanitizeLog(r.Method)+" "+sanitizeLog(r.URL.Path),
					"panic", rec)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	}
}

// CSP returns a [via.Middleware] that generates a fresh nonce per
// request, sets a Content-Security-Policy header that allows only
// same-origin scripts plus that nonce, and threads the nonce through
// r.Context so [via.Ctx.CSPNonce] returns the matching value.
//
// The policy includes 'unsafe-eval' because Via's bundled Datastar
// runtime compiles every data-* expression and event handler with
// Function() at runtime, which CSP gates behind that keyword. In this
// policy 'unsafe-eval' only authorizes eval/Function inside script the
// policy has already admitted — same-origin files and nonce-carrying
// tags. It does not re-enable inline script injection: inline tags
// without the per-request nonce stay blocked. Omitting the keyword is
// not a hardening option today; it makes every handler throw
// EvalError on first use (see docs/troubleshooting.md).
//
// Wire it up as the very first middleware so the header lands on
// every response, including 404s and SSE handshakes:
//
//	app.Use(mw.CSP())
//
// Pass extra directives if defaults aren't enough:
//
//	app.Use(mw.CSP("img-src 'self' data:"))
//
// The default policy is `default-src 'self'; script-src 'self'
// 'nonce-XYZ' 'unsafe-eval'; object-src 'none'; base-uri 'self';
// frame-ancestors 'self'`.
func CSP(extra ...string) via.Middleware {
	tail := ""
	for _, d := range extra {
		tail += "; " + d
	}
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		n := randID()
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'nonce-"+n+"' 'unsafe-eval'; "+
				"object-src 'none'; base-uri 'self'; frame-ancestors 'self'"+tail)

		next.ServeHTTP(w, via.RequestWithCSPNonce(r, n))
	}
}

// HSTS returns a [via.Middleware] that sets the
// Strict-Transport-Security response header. Complements the
// Secure-by-default session cookie for HTTPS deployments. Use this only when
// the app is actually served over HTTPS — sending HSTS over plain
// HTTP gets ignored, but enabling it behind a misconfigured TLS
// terminator can lock users out for the max-age duration.
//
// Defaults: max-age=31536000 (one year), includeSubDomains.
//
//	app.Use(mw.HSTS())                       // 1y, subdomains, no preload
//	app.Use(mw.HSTS(mw.HSTSPreload(true)))   // opt into preload list
func HSTS(opts ...HSTSOption) via.Middleware {
	cfg := hstsConfig{maxAge: 31536000, includeSubdomains: true}
	for _, o := range opts {
		o(&cfg)
	}
	header := "max-age=" + strconv.Itoa(cfg.maxAge)
	if cfg.includeSubdomains {
		header += "; includeSubDomains"
	}
	if cfg.preload {
		header += "; preload"
	}
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		w.Header().Set("Strict-Transport-Security", header)
		next.ServeHTTP(w, r)
	}
}

type hstsConfig struct {
	maxAge            int
	includeSubdomains bool
	preload           bool
}

// HSTSOption configures [HSTS].
type HSTSOption func(*hstsConfig)

// HSTSMaxAge overrides the max-age value (in seconds).
func HSTSMaxAge(seconds int) HSTSOption {
	return func(c *hstsConfig) { c.maxAge = seconds }
}

// HSTSIncludeSubdomains toggles the includeSubDomains directive.
func HSTSIncludeSubdomains(on bool) HSTSOption {
	return func(c *hstsConfig) { c.includeSubdomains = on }
}

// HSTSPreload toggles the preload directive. Only set true if you
// actually intend to submit to the HSTS preload list — once preloaded
// the policy is essentially irreversible.
func HSTSPreload(on bool) HSTSOption {
	return func(c *hstsConfig) { c.preload = on }
}

// RedirectHTTPS returns a [via.Middleware] that 301-redirects plain
// HTTP to the same URL on https. Detection respects the
// X-Forwarded-Proto header (the convention every TLS-terminating
// proxy / load balancer sets), falling back to r.TLS != nil for
// direct-bind scenarios.
//
//	app.Use(mw.RedirectHTTPS())
//
// The redirect is applied to every request; pair with [HSTS] for a
// complete TLS-only deployment posture (the session cookie is Secure
// by default).
//
// Security caveat: X-Forwarded-Proto is trusted unconditionally.
// Deploy this middleware ONLY behind a trusted reverse proxy / load
// balancer that overwrites the header on inbound requests. If the
// app is exposed directly (no fronting proxy) a client can send
// X-Forwarded-Proto: https over plain HTTP and bypass the redirect
// entirely — every subsequent request stays unprotected. When binding
// directly to :443 + :80, use [RedirectHTTPSStrict] instead.
func RedirectHTTPS() via.Middleware {
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if isHTTPS(r) {
			next.ServeHTTP(w, r)
			return
		}
		http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(),
			http.StatusMovedPermanently)
	}
}

// RedirectHTTPSStrict is the direct-bind variant of [RedirectHTTPS]:
// it ignores X-Forwarded-Proto entirely and only treats r.TLS != nil
// as HTTPS. Use this when the app listens on :443 + :80 itself, with
// no reverse proxy in front — a client cannot forge r.TLS, so this
// closes the bypass that [RedirectHTTPS]'s header trust opens.
//
// Use [RedirectHTTPS] (not Strict) behind a trusted proxy that
// terminates TLS and overwrites X-Forwarded-Proto on inbound requests.
func RedirectHTTPSStrict() via.Middleware {
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.TLS != nil {
			next.ServeHTTP(w, r)
			return
		}
		http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(),
			http.StatusMovedPermanently)
	}
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

// randID returns a 16-byte (128-bit) base64-url-no-padding token used
// for request ids and CSP nonces. Panics on crypto/rand failure — that
// would mean a broken OS entropy source, not a recoverable runtime
// condition.
func randID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("via/mw: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

type statusWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.written {
		s.status = code
		s.written = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if !s.written {
		s.written = true
	}
	return s.ResponseWriter.Write(b)
}

// Flush forwards if the wrapped writer supports it. SSE streams need
// this so frames reach the browser without buffering.
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the wrapped writer so http.ResponseController can reach
// optional interfaces (Hijacker, ReaderFrom, …) this wrapper doesn't itself
// implement — without it, wrapping every request in statusWriter would
// silently disable hijacking and the sendfile fast-path on the underlying writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// sanitizeLog strips CR/LF from values that flow from request data into
// log lines, preventing forged log entries (CWE-117).
func sanitizeLog(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}
