package via

import (
	"context"
	"fmt"
	"log"
	"log/slog"
)

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
	return LoggerFunc(func(level LogLevel, msg string, kv ...any) {
		if level < app.cfg.logLevel {
			return
		}
		if tab != "" {
			kv = append([]any{"via_tab", tab}, kv...)
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
