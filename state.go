package via

import (
	"fmt"
	"runtime"
	"sync"
)

// Scope defines the lifetime and sharing boundary of a State value.
type Scope int

const (
	// ScopeTab is the default scope — state is local to a single browser tab/session.
	ScopeTab Scope = iota
	// ScopeSession scopes state to a user session across tabs (not yet implemented).
	ScopeSession
	// ScopeApp scopes state globally across all sessions and tabs.
	ScopeApp
)

// StateOption configures a State at construction time.
type StateOption func(*stateConfig)

type stateConfig struct {
	scope    Scope
	scopeSet bool
}

// WithScopeApp makes the state shared across all sessions and tabs.
func WithScopeApp() StateOption {
	return func(cfg *stateConfig) {
		if cfg.scopeSet {
			panic("conflicting scopes: multiple scope options provided")
		}
		cfg.scope = ScopeApp
		cfg.scopeSet = true
	}
}

// WithScopeSession makes the state scoped to a user session (not yet implemented).
func WithScopeSession() StateOption {
	return func(cfg *stateConfig) {
		if cfg.scopeSet {
			panic("conflicting scopes: multiple scope options provided")
		}
		cfg.scope = ScopeSession
		cfg.scopeSet = true
	}
}

// stateOf is the generic state implementation.
type stateOf[T any] struct {
	id    string
	val   T
	scope Scope
	dirty bool
	mu    sync.Mutex
}

// Dirty reports whether the state has been modified since construction.
func (s *stateOf[T]) Dirty() bool { return s.dirty }

// Get returns the current value of the state.
func (s *stateOf[T]) Get(_ *Context) T {
	if s.scope == ScopeApp {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	return s.val
}

// Set updates the state value and marks it dirty.
func (s *stateOf[T]) Set(c *Context, v T) {
	if s.scope == ScopeApp {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	s.val = v
	s.dirty = true
	if c != nil {
		c.markStateModified()
	}
}

// State creates a new State with the given initial value.
// Default scope is ScopeTab (local to this browser tab).
func State[T any](c *Context, initial T, opts ...StateOption) *stateOf[T] {
	cfg := &stateConfig{scope: ScopeTab}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.scope == ScopeSession {
		panic("ScopeSession is not yet implemented (deferred to design 07)")
	}

	if cfg.scope == ScopeApp {
		// Use caller's PC as a stable key so all page instances share the same state.
		var pcs [1]uintptr
		runtime.Callers(2, pcs[:])
		key := pcs[0]

		if existing, ok := c.app.appStateStore.Load(key); ok {
			return existing.(*stateOf[T])
		}
		s := &stateOf[T]{
			id:    fmt.Sprintf("%x", key),
			val:   initial,
			scope: ScopeApp,
		}
		actual, _ := c.app.appStateStore.LoadOrStore(key, s)
		return actual.(*stateOf[T])
	}

	// ScopeTab — state is owned by the closure, no storage needed on context.
	return &stateOf[T]{
		id:    genRandID(),
		val:   initial,
		scope: ScopeTab,
	}
}
