package via

import (
	"crypto/rand"
	"encoding/hex"
)

// genSecureID returns a 256-bit hex string for security-critical IDs (sessions).
func genSecureID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("rand.Read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func genRandID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("rand.Read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
