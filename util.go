package via

import (
	"crypto/rand"
	"encoding/hex"
)

func genSecureID() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
