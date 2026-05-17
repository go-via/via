package via

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// Middleware is the request-wrapping function shape used by App.Use. Each
// middleware receives the next handler in the chain and decides whether to
// invoke it, short-circuit (e.g. with a 401), or wrap the response writer
// before passing through. Registration order is outer-first: the first
// middleware passed to Use runs first per request.
type Middleware func(w http.ResponseWriter, r *http.Request, next http.Handler)

func applyMiddleware(chain []Middleware, final http.Handler) http.Handler {
	// Wrap from the inside out so chain[0] ends up as the outermost
	// middleware and runs first per request — the canonical Go pattern.
	for i := len(chain) - 1; i >= 0; i-- {
		mw, next := chain[i], final
		final = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mw(w, r, next)
		})
	}
	return final
}

// Defaults installs the recommended middleware stack on the app:
// RequestID (X-Request-ID stamping), AccessLog (one info line per
// request with the captured status + rid), Recover (panic → 500).
// Order matters: RequestID is outermost so AccessLog can read the
// id from r.Context; AccessLog wraps Recover so it sees the final
// status (500 after Recover writes) on the deferred log line.
//
//	app := via.New()
//	via.Defaults(app)
//	via.Mount[Counter](app, "/")
func Defaults(a *App) {
	a.Use(RequestID(), AccessLog(a), Recover(a))
}

// RequestID returns a Middleware that ensures every request carries an
// X-Request-ID — using the inbound header value if present, otherwise
// generating a fresh 16-byte base64url id. The id is mirrored back on
// the response so clients can quote it when reporting issues.
//
//	app.Use(via.RequestID())
//	app.Use(via.AccessLog(app))   // sees the same id in subsequent logs
//
// The id is also planted on r.Context under requestIDKey{}; via.Log
// includes it in the kv pairs when present.
func RequestID() Middleware {
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = genCSPNonce() // 16-byte base64url — same shape, different purpose
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(
			context.WithValue(r.Context(), requestIDKey{}, id)))
	}
}

type requestIDKey struct{}

// RequestIDFrom pulls the request id out of r.Context. Returns "" if
// no RequestID middleware has run for this request.
func RequestIDFrom(r *http.Request) string {
	if r == nil {
		return ""
	}
	v, _ := r.Context().Value(requestIDKey{}).(string)
	return v
}

// RedirectHTTPS returns a Middleware that 301-redirects plain HTTP to
// the same URL on https. Detection respects the X-Forwarded-Proto
// header (the convention every TLS-terminating proxy / load balancer
// sets), falling back to r.TLS != nil for direct-bind scenarios.
//
//	app.Use(via.RedirectHTTPS())
//
// The redirect is applied to every request; pair with WithSecureCookies
// and HSTS for a complete TLS-only deployment posture.
//
// Security caveat: X-Forwarded-Proto is trusted unconditionally. Deploy
// this middleware ONLY behind a trusted reverse proxy / load balancer
// that overwrites the header on inbound requests. If the app is exposed
// directly (no fronting proxy) a client can send X-Forwarded-Proto:
// https over plain HTTP and bypass the redirect entirely — every
// subsequent request stays unprotected. When binding directly to :443
// + :80, use HTTPS detection via r.TLS instead by stripping the header
// in your own middleware before this one runs.
func RedirectHTTPS() Middleware {
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if isHTTPS(r) {
			next.ServeHTTP(w, r)
			return
		}
		target := "https://" + r.Host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	}
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

// HSTS returns a Middleware that sets the Strict-Transport-Security
// response header. Pairs with via.WithSecureCookies for HTTPS
// deployments. Use this only when the app is actually served over
// HTTPS — sending HSTS over plain HTTP gets ignored, but enabling it
// behind a misconfigured TLS terminator can lock users out for the
// max-age duration.
//
// Defaults: max-age=31536000 (one year), includeSubDomains.
//
//	app.Use(via.HSTS())                     // 1y, subdomains, no preload
//	app.Use(via.HSTS(via.HSTSPreload(true))) // opt into preload list
func HSTS(opts ...HSTSOption) Middleware {
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

// HSTSOption configures HSTS.
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

// Recover returns a Middleware that catches panics in downstream
// handlers, logs the recovered value through the App's logger, and
// writes a 500 response so the goroutine doesn't crash the server.
//
// Action handlers already have per-action panic recovery (so action
// panics surface through WithActionErrorHandler / the default alert).
// Recover protects everything else — non-via handlers via HandleFunc,
// custom middleware, plugin endpoints — that wouldn't otherwise have
// a backstop:
//
//	app.Use(via.Recover(app))
func Recover(a *App) Middleware {
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		defer func() {
			if rec := recover(); rec != nil {
				a.logErr(nil, "panic in handler %s %s: %v", r.Method, r.URL.Path, rec)
				// If the handler already wrote headers, http.Error
				// will be a no-op. Either way the goroutine survives.
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	}
}

// AccessLog returns a Middleware that emits one info-level log record
// per HTTP request through the App's configured Logger:
//
//	app.Use(via.AccessLog(app))
//
// Format: method=GET path=/foo status=200 duration=1.2ms remote=…
// Status is captured by wrapping the ResponseWriter; default 200 if
// the handler never calls WriteHeader.
func AccessLog(a *App) Middleware {
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		dur := time.Since(start)
		if rid := RequestIDFrom(r); rid != "" {
			a.logInfo(nil, "%s %s status=%d duration=%s rid=%s",
				r.Method, r.URL.Path, sw.status, dur, rid)
		} else {
			a.logInfo(nil, "%s %s status=%d duration=%s",
				r.Method, r.URL.Path, sw.status, dur)
		}
	}
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
