package via

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"strings"
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
// otherwise has no App attached. Use it inside actions / OnInit /
// OnConnect to write app-level structured logs through the same pipe
// via uses for its own warnings:
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
	if r := ctx.Request(); r != nil {
		rid = RequestIDFrom(r)
	}
	return LoggerFunc(func(level LogLevel, msg string, kv ...any) {
		if level < app.cfg.logLevel {
			return
		}
		// Prepend correlation pairs in one allocation. The previous
		// implementation made a 4-cap head slice unconditionally, then
		// appended kv into it (a second alloc whenever both correlation
		// pairs were present). Sizing the slice exactly avoids both.
		extra := 0
		if tab != "" {
			extra += 2
		}
		if rid != "" {
			extra += 2
		}
		if extra == 0 {
			base.Log(level, msg, kv...)
			return
		}
		full := make([]any, 0, extra+len(kv))
		if tab != "" {
			full = append(full, tabSignalKey, tab)
		}
		if rid != "" {
			full = append(full, "rid", rid)
		}
		full = append(full, kv...)
		base.Log(level, msg, full...)
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
		log.Printf("[%s] %s", levelTag(level), stripCRLF(msg))
		return
	}
	var sb strings.Builder
	sb.WriteByte('[')
	sb.WriteString(levelTag(level))
	sb.WriteString("] ")
	sb.WriteString(msg)
	for i := 0; i+1 < len(kv); i += 2 {
		k, _ := kv[i].(string)
		sb.WriteByte(' ')
		sb.WriteString(k)
		sb.WriteByte('=')
		fmt.Fprintf(&sb, "%v", kv[i+1])
	}
	log.Print(stripCRLF(sb.String()))
}

// stripCRLF removes CR/LF from a log line so user-controlled values
// can't forge new log entries (CWE-117).
func stripCRLF(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
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
