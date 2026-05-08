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

// genCSPNonce returns a 16-byte base64 (URL-safe, no padding) string
// for strict-CSP nonce attributes. 16 bytes = 128 bits of entropy is
// the OWASP recommendation.
func genCSPNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64URL(b)
}

func base64URL(b []byte) string {
	const tbl = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	out := make([]byte, 0, (len(b)*4+2)/3)
	for i := 0; i+3 <= len(b); i += 3 {
		x := uint32(b[i])<<16 | uint32(b[i+1])<<8 | uint32(b[i+2])
		out = append(out, tbl[x>>18&63], tbl[x>>12&63], tbl[x>>6&63], tbl[x&63])
	}
	switch len(b) % 3 {
	case 1:
		x := uint32(b[len(b)-1]) << 16
		out = append(out, tbl[x>>18&63], tbl[x>>12&63])
	case 2:
		x := uint32(b[len(b)-2])<<16 | uint32(b[len(b)-1])<<8
		out = append(out, tbl[x>>18&63], tbl[x>>12&63], tbl[x>>6&63])
	}
	return string(out)
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
