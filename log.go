package via

import (
	"fmt"
	"log"
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

// LoggerFunc adapts a function into a Logger.
type LoggerFunc func(level LogLevel, msg string, kv ...any)

// Log implements Logger.
func (f LoggerFunc) Log(level LogLevel, msg string, kv ...any) { f(level, msg, kv...) }

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
