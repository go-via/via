package via

import "sync"

// kvStore is a string-keyed store with an atomic Update path. Plain
// Load/Store/Delete/Range mirror sync.Map; Update holds a per-key
// mutex across the load → fn → store sequence so read-modify-write
// from multiple goroutines doesn't lose increments. Backs the
// StateApp app store and per-session data stores, where concurrent
// writers from different ctxs race on the same key.
type kvStore struct {
	values sync.Map
	locks  sync.Map
}

func (s *kvStore) Load(key string) (any, bool)  { return s.values.Load(key) }
func (s *kvStore) Store(key string, v any)      { s.values.Store(key, v) }
func (s *kvStore) Delete(key string)            { s.values.Delete(key) }
func (s *kvStore) Range(fn func(k, v any) bool) { s.values.Range(fn) }

// Update atomically applies fn to the current value for key. fn
// receives the old value (or nil if absent) and returns (new, err).
// On non-nil error the store is unchanged and the error is returned
// to the caller. Held under a per-key mutex for the duration of fn —
// fn must not call back into this store on the same key.
func (s *kvStore) Update(key string, fn func(old any) (any, error)) (any, error) {
	m := s.lockFor(key)
	m.Lock()
	defer m.Unlock()
	old, _ := s.values.Load(key)
	next, err := fn(old)
	if err != nil {
		return old, err
	}
	s.values.Store(key, next)
	return next, nil
}

func (s *kvStore) lockFor(key string) *sync.Mutex {
	if m, ok := s.locks.Load(key); ok {
		return m.(*sync.Mutex)
	}
	m, _ := s.locks.LoadOrStore(key, &sync.Mutex{})
	return m.(*sync.Mutex)
}
