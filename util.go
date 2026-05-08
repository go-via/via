package via

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

func fmtSprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

func genSecureID() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func nowNano() int64 { return time.Now().UnixNano() }

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// sprintfFmt mirrors fmt.Sprintf without forcing every caller of
// ctx.ExecScriptf to import fmt themselves through h.
func sprintfFmt(format string, args ...any) string { return fmtSprintf(format, args...) }

func strconvAppendInt(n int64) string   { return strconv.FormatInt(n, 10) }
func strconvAppendUint(n uint64) string { return strconv.FormatUint(n, 10) }
func strconvAppendFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
