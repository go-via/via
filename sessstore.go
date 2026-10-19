package via

import (
	"context"
	"sync"
	"time"
)

// SessionStore is where session state lives between requests. The default is a
// process-local map, which is why a restart logs everyone out and a second pod
// sees none of the first pod's sessions; point [WithSessionStore] at Redis, a
// SQL table, or anything else that outlives the process and both stop being
// true.
//
// A store is a blob map and nothing more. via hands each session over
// already-serialized and never asks a store to understand it, so there is no
// session type to implement against:
//
//   - Rotation is Save under the new id then Delete of the old one; a store
//     implements neither.
//   - Expiry is the ttl handed to Save. Honour it if the backend does it for
//     free (Redis SETEX, a SQL expires_at column); via stamps the blob with the
//     same deadline and refuses an expired Load regardless, so a store that
//     ignores ttl is still correct — it just keeps dead rows around.
//   - The idle window slides: via re-Saves a session once less than half its
//     TTL is left, so a store never has to touch expiry on Load.
//
// Every method may be called concurrently, and from a request goroutine — honour
// ctx. A failed Load answers every transport with 503 and [ErrStoreDown]
// rather than serving the request as anonymous. A failed Save or Delete is
// logged and the write dropped. [Session.Rotate] is the exception: if the
// session cannot be written under its new id, or the old id can be neither
// deleted nor overwritten with an expired blob, via panics and the request
// answers 500 rather than reporting a rotation that did not happen.
//
// Implement [VersionedSessionStore] as well if the backend can do a conditional
// write; without it, two requests writing the same session at the same instant
// can still lose one.
//
// Every open stream bound to a session calls Load once per keepalive (every
// 25s), so a Rotate, logout or expiry on another process reaches its tabs:
// open streams / 25 Loads a second. Against a database each is a query, so
// size the pool for them or put a cache in front. The Load runs on the tab's
// own goroutine, so a slow one delays that tab's next push; a failed Load
// keeps the stream up and asks again on the next beat.
type SessionStore interface {
	// Load returns the blob stored under id. ok is false when id is unknown or
	// expired. err is for backend failures only.
	Load(ctx context.Context, id string) (data []byte, ok bool, err error)
	// Save writes data under id and arms its expiry ttl from now, replacing any
	// blob already there.
	Save(ctx context.Context, id string, data []byte, ttl time.Duration) error
	// Delete removes id. Deleting an id that is not there is not an error.
	Delete(ctx context.Context, id string) error
}

// VersionedSessionStore is the optional half of [SessionStore], for a backend that
// can make a write conditional on the revision it read. via writes a session by
// re-reading the stored blob and overlaying what this request wrote; with a
// plain store that read-modify-write is not atomic, so a write can land on a
// session another request deleted or rotated a moment before. Implement this and via retries
// the merge until its write applies to the revision it merged against, which
// closes the window entirely.
//
// version is opaque and store-defined; 0 means "no blob stored". Redis does this
// with WATCH/MULTI or a Lua script, SQL with an UPDATE ... WHERE version = $n.
// The default memory store implements it.
type VersionedSessionStore interface {
	SessionStore
	// LoadVersion is Load, plus the revision token of the blob returned.
	LoadVersion(ctx context.Context, id string) (data []byte, version uint64, ok bool, err error)
	// SaveIf is Save, applied only while the stored revision is still version.
	// ok is false — with a nil error — when it moved on and the caller must
	// re-read and retry.
	SaveIf(ctx context.Context, id string, data []byte, ttl time.Duration, version uint64) (ok bool, err error)
}

// NewMemorySessionStore returns the default process-local store: a map that is
// lost on restart and invisible to every other pod. Use it explicitly only to
// make that choice visible at the call site.
//
// It returns the concrete type, not the [SessionStore] interface: the store
// also implements [VersionedSessionStore], and a wrapper built around the
// interface would silently drop the CAS path and take the lossy merge instead.
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{m: map[string]memoryEntry{}}
}

type memoryEntry struct {
	data []byte
	exp  time.Time
	ver  uint64
}

// MemorySessionStore is the process-local default store — a map guarded by a
// mutex, with CAS support ([VersionedSessionStore]). Build one with
// [NewMemorySessionStore]; the zero value is not usable.
type MemorySessionStore struct {
	mu     sync.Mutex
	m      map[string]memoryEntry
	writes int
	ver    uint64
}

// Load implements [SessionStore]. An entry past its TTL is deleted on sight
// and reported absent.
func (s *MemorySessionStore) Load(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[id]
	if !ok {
		return nil, false, nil
	}
	if !e.exp.IsZero() && time.Now().After(e.exp) {
		delete(s.m, id)
		return nil, false, nil
	}
	return e.data, true, nil
}

// LoadVersion implements [VersionedSessionStore]. The revision is a
// process-wide counter, so it changes on any write, not only this id's.
func (s *MemorySessionStore) LoadVersion(_ context.Context, id string) ([]byte, uint64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[id]
	if !ok {
		return nil, 0, false, nil
	}
	if !e.exp.IsZero() && time.Now().After(e.exp) {
		delete(s.m, id)
		return nil, 0, false, nil
	}
	return e.data, e.ver, true, nil
}

// SaveIf implements [VersionedSessionStore]. An absent or expired entry reads
// as version 0, so a first write must pass 0.
func (s *MemorySessionStore) SaveIf(_ context.Context, id string, data []byte, ttl time.Duration, version uint64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var cur uint64
	if e, ok := s.m[id]; ok && (e.exp.IsZero() || !time.Now().After(e.exp)) {
		cur = e.ver
	}
	if cur != version {
		return false, nil
	}
	s.store(id, data, ttl)
	return true, nil
}

// Save implements [SessionStore]. A ttl of 0 or less stores the blob without
// an expiry.
func (s *MemorySessionStore) Save(_ context.Context, id string, data []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store(id, data, ttl)
	return nil
}

// store writes under the held lock and stamps a fresh revision.
func (s *MemorySessionStore) store(id string, data []byte, ttl time.Duration) {
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	s.ver++
	s.m[id] = memoryEntry{data: data, exp: exp, ver: s.ver}
	// Expiry is otherwise lazy-on-read, so a session nobody ever revisits is
	// never reclaimed. Amortised over writes rather than per write: the sweep
	// is O(n) and a login-heavy burst would otherwise pay it every time.
	s.writes++
	if s.writes%memorySweepEvery == 0 {
		now := time.Now()
		for k, e := range s.m {
			if !e.exp.IsZero() && now.After(e.exp) {
				delete(s.m, k)
			}
		}
	}
}

// Delete implements [SessionStore]. Deleting an absent id is not an error.
func (s *MemorySessionStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

const memorySweepEvery = 256
