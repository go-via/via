package via

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"reflect"
	"strconv"
)

func genSecureID() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing means the OS entropy source is broken —
		// not a recoverable runtime condition. Falling back silently
		// would produce all-zero (predictable) session/tab ids, which
		// is strictly worse than aborting.
		panic("via: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// validSessionID reports whether s is exactly the shape genSecureID produces:
// 64 lowercase hex characters (32 random bytes). A pod will adopt a presented
// session sid only if it passes this check, so a client can never make a pod
// register an arbitrary attacker-chosen or malformed token as a session.
func validSessionID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// genCSPNonce returns a 16-byte base64 (URL-safe, no padding) string
// for strict-CSP nonce attributes. 16 bytes = 128 bits of entropy is
// the OWASP recommendation.
func genCSPNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("via: crypto/rand failed: " + err.Error())
	}
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

// decodeScalarString writes raw into the scalar field v according to its
// kind. Parse failures leave the field at its zero value — used for path
// params, query params, and form-field decoding where best-effort beats
// rejecting the request.
func decodeScalarString(v reflect.Value, kind reflect.Kind, raw string) {
	switch kind {
	case reflect.String:
		v.SetString(raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			v.SetInt(n)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
			v.SetUint(n)
		}
	case reflect.Float32, reflect.Float64:
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			v.SetFloat(f)
		}
	case reflect.Bool:
		if b, err := strconv.ParseBool(raw); err == nil {
			v.SetBool(b)
		}
	}
}
