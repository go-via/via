package via

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"
)

func genSecureID() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func nowNano() int64 { return time.Now().UnixNano() }

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func strconvAppendInt(n int64) string  { return strconv.FormatInt(n, 10) }
func strconvAppendUint(n uint64) string { return strconv.FormatUint(n, 10) }
func strconvAppendFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
