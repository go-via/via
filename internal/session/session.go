// Package session owns the two formats a via session is stored and carried in:
// the blob a SessionStore holds and the signed cookie value that names it.
package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// Blob is the stored form of a session. Field names are short because every
// request that writes re-encodes the whole thing.
type Blob struct {
	SID  string                     `json:"sid"`
	Exp  int64                      `json:"exp"` // unix nanos; via's own expiry check, independent of the store's
	Vals map[string]json.RawMessage `json:"v"`
}

// State is what a stored blob is worth to a reader.
type State int

const (
	// Invalid is a blob that does not decode or names no session.
	Invalid State = iota
	// Expired is a well-formed blob past its own expiry. A store that ignores
	// the ttl it was handed must not resurrect it.
	Expired
	Live
)

// Decode reads a stored blob. Callers that know which session they expect
// still compare SID themselves: an id can be recycled under another session.
func Decode(raw []byte) (Blob, State) {
	var b Blob
	if json.Unmarshal(raw, &b) != nil || b.SID == "" {
		return b, Invalid
	}
	if b.Exp > 0 && time.Now().After(b.Expiry()) {
		return b, Expired
	}
	return b, Live
}

// Expiry is b's idle deadline; the zero Exp of a blob that never expires
// reads as the Unix epoch.
func (b Blob) Expiry() time.Time { return time.Unix(0, b.Exp) }

// Encode writes b in its stored form.
func Encode(b Blob) ([]byte, error) { return json.Marshal(b) }

// Tombstone is a blob for sid that is already expired: written over an id
// that cannot be deleted, it reads as gone.
func Tombstone(sid string) []byte {
	raw, _ := json.Marshal(Blob{SID: sid, Exp: time.Now().Add(-time.Minute).UnixNano()})
	return raw
}

// Signer signs session ids into cookie values, so a tampered id is rejected.
type Signer struct{ key []byte }

func NewSigner(key []byte) Signer { return Signer{key: key} }

// Value is the cookie value that carries id.
func (s Signer) Value(id string) string { return id + "." + s.sign(id) }

// Verify returns the id a cookie value carries when its signature holds.
func (s Signer) Verify(value string) (id string, ok bool) {
	i := strings.LastIndexByte(value, '.')
	if i < 0 {
		return "", false
	}
	id, sig := value[:i], value[i+1:]
	if !hmac.Equal([]byte(sig), []byte(s.sign(id))) {
		return "", false
	}
	return id, true
}

func (s Signer) sign(id string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
