package via

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
)

func genSecureID() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// genCSPNonce returns a 16-byte base64 (URL-safe, no padding) string
// for strict-CSP nonce attributes. 16 bytes = 128 bits of entropy is
// the OWASP recommendation.
func genCSPNonce() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// lowerFirst returns name with its leading ASCII letter lowercased. One
// allocation: the byte→string conversion at return. Used by tag-key
// derivation (Mount-time wire keys, form-field default keys) to convert
// "FieldName" → "fieldName" without strings.ToLower's slice + concat.
func lowerFirst(name string) string {
	b := []byte(name)
	if len(b) > 0 && b[0] >= 'A' && b[0] <= 'Z' {
		b[0] += 'a' - 'A'
	}
	return string(b)
}
