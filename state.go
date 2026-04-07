package via

import (
	"fmt"
	"runtime"
	"sync"
)

// Scope defines the lifetime and sharing boundary of a State value.
type Scope int

const (
	ScopeTab Scope = iota // Each browser tab owns an independent copy.
	ScopeApp              // Shared across all tabs and sessions.
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

// stateOf is the generic state implementation.
type stateOf[T any] struct {
	id    string
	val   T
	scope Scope
	mu    sync.Mutex
}

// Get returns the current value of the state.
func (s *stateOf[T]) Get(ctx *Ctx) T {
	if s.scope == ScopeApp {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	return s.val
}

// Set updates the state value and marks it dirty.
func (s *stateOf[T]) Set(ctx *Ctx, v T) {
	if s.scope == ScopeApp {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	s.val = v
	if ctx != nil {
		ctx.markStateModified()
	}
}

// State creates a new State with the given initial value.
func State[T any](cmp *Cmp, initial T, opts ...StateOption) *stateOf[T] {
	cfg := &stateConfig{scope: ScopeTab}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.scope == ScopeApp {
		var pcs [1]uintptr
		runtime.Callers(2, pcs[:])
		key := pcs[0]

		if existing, ok := cmp.appStateStore.Load(key); ok {
			return existing.(*stateOf[T])
		}
		s := &stateOf[T]{
			id:    fmt.Sprintf("%x", key),
			val:   initial,
			scope: ScopeApp,
		}
		actual, _ := cmp.appStateStore.LoadOrStore(key, s)
		return actual.(*stateOf[T])
	}

	return &stateOf[T]{
		id:    genRandID(),
		val:   initial,
		scope: ScopeTab,
	}
}
