package via

import (
	"crypto/rand"
	"encoding/hex"
)

func genRandID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("rand.Read failed: " + err.Error())
	}
	return hex.EncodeToString(b)[:8]
}
