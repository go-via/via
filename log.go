package via

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"time"
)

func timeNow() time.Time            { return time.Now() }
func timeSince(t time.Time) string  { return time.Since(t).String() }

// Logger receives log records produced by the via runtime. Implementations
// are free to forward to any logger of their choice — slog, zap, zerolog,
// a test buffer, /dev/null. The default logger writes to log.Printf with
// a "[level]" prefix.
//
// Field pairs are appended after the message:
//
//	logger.Log(LogError, "action failed", "via_tab", id, "name", "Inc")
//	→ default output: [error] action failed via_tab=… name=Inc
//
// Field values may be any type; the default formatter renders with %v.
type Logger interface {
	Log(level LogLevel, msg string, kv ...any)
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
	a.Use(RequestID())
	a.Use(AccessLog(a))
	a.Use(Recover(a))
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
				// will be a noop. Either way the goroutine survives.
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
		start := timeNow()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if rid := RequestIDFrom(r); rid != "" {
			a.logInfo(nil, "%s %s status=%d duration=%s rid=%s",
				r.Method, r.URL.Path, sw.status, timeSince(start), rid)
		} else {
			a.logInfo(nil, "%s %s status=%d duration=%s",
				r.Method, r.URL.Path, sw.status, timeSince(start))
		}
	}
}

type statusWriter struct {
	http.ResponseWriter
	status   int
	written  bool
	hijacked bool
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

// Log returns the logger configured on the App that owns ctx, stamped
// with the current via_tab so every record is correlated to the tab
// that produced it. Falls back to the default logger if ctx is nil or
// detached (e.g. constructed via test.NewCtx without an App attached).
// Use it inside actions / Init / OnConnect to write app-level
// structured logs through the same pipe via uses for its own warnings:
//
//	via.Log(ctx).Log(via.LogInfo, "checkout", "user", id, "amount", n)
func Log(ctx *Ctx) Logger {
	if ctx == nil || ctx.app == nil {
		return defaultLogger{}
	}
	app := ctx.app
	tab := ctx.id
	base := app.cfg.logger
	if base == nil {
		base = defaultLogger{}
	}
	rid := ""
	if ctx.r != nil {
		rid = RequestIDFrom(ctx.r)
	}
	return LoggerFunc(func(level LogLevel, msg string, kv ...any) {
		if level < app.cfg.logLevel {
			return
		}
		// Prepend correlation pairs so they're stable in slog handlers
		// that rely on attribute order.
		head := make([]any, 0, 4)
		if tab != "" {
			head = append(head, "via_tab", tab)
		}
		if rid != "" {
			head = append(head, "rid", rid)
		}
		if len(head) > 0 {
			kv = append(head, kv...)
		}
		base.Log(level, msg, kv...)
	})
}

// LoggerFunc adapts a function into a Logger.
type LoggerFunc func(level LogLevel, msg string, kv ...any)

// Log implements Logger.
func (f LoggerFunc) Log(level LogLevel, msg string, kv ...any) { f(level, msg, kv...) }

// SlogLogger adapts a *slog.Logger to via's Logger. via's level maps
// onto slog's directly (Debug, Info, Warn, Error). Field pairs are
// passed through as slog attrs.
//
//	app := via.New(via.WithLogger(via.SlogLogger(slog.Default())))
func SlogLogger(l *slog.Logger) Logger {
	if l == nil {
		l = slog.Default()
	}
	return LoggerFunc(func(level LogLevel, msg string, kv ...any) {
		l.LogAttrs(context.Background(), slogLevel(level), msg, attrsFromKV(kv)...)
	})
}

func slogLevel(l LogLevel) slog.Level {
	switch l {
	case LogDebug:
		return slog.LevelDebug
	case LogInfo:
		return slog.LevelInfo
	case LogWarn:
		return slog.LevelWarn
	case LogError:
		return slog.LevelError
	}
	return slog.LevelInfo
}

func attrsFromKV(kv []any) []slog.Attr {
	if len(kv) == 0 {
		return nil
	}
	out := make([]slog.Attr, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		key, _ := kv[i].(string)
		out = append(out, slog.Any(key, kv[i+1]))
	}
	return out
}

// defaultLogger writes to the standard log package.
type defaultLogger struct{}

func (defaultLogger) Log(level LogLevel, msg string, kv ...any) {
	if len(kv) == 0 {
		log.Printf("[%s] %s", levelTag(level), msg)
		return
	}
	var sb fmtBuf
	sb.WriteString("[")
	sb.WriteString(levelTag(level))
	sb.WriteString("] ")
	sb.WriteString(msg)
	for i := 0; i+1 < len(kv); i += 2 {
		k, _ := kv[i].(string)
		sb.WriteString(" ")
		sb.WriteString(k)
		sb.WriteString("=")
		fmt.Fprintf(&sb, "%v", kv[i+1])
	}
	log.Print(sb.String())
}

func levelTag(l LogLevel) string {
	switch l {
	case LogDebug:
		return "debug"
	case LogInfo:
		return "info"
	case LogWarn:
		return "warn"
	case LogError:
		return "error"
	}
	return "info"
}

// fmtBuf is a tiny buffer that satisfies io.Writer for fmt.Fprintf without
// pulling bytes.Buffer into log.go's import set.
type fmtBuf struct{ b []byte }

func (s *fmtBuf) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }
func (s *fmtBuf) WriteString(p string)        { s.b = append(s.b, p...) }
func (s *fmtBuf) String() string              { return string(s.b) }
