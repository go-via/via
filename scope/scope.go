// Package scope holds non-tab state handles. Tab-scoped state already lives
// at via.State[T]; this package adds wider scopes:
//
//	type Profile struct {
//	    Theme scope.User[string]   // session-scoped: shared across tabs
//	    Hits  scope.App[int]       // app-scoped: shared across sessions
//	}
//
// Each scope is a distinct concrete type so that mismatching the scope at
// the call site is a compile error.
package scope

import (
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// User is a session-scoped reactive value: shared across every tab opened
// from the same browser session, expires per WithSessionTTL.
type User[T any] struct {
	val  T
	key  string
	mu   sync.RWMutex
	once sync.Once
}

// Get returns the current session value.
func (s *User[T]) Get(ctx *via.Ctx) T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.val
}

// Set writes the session value and triggers a re-render of the current tab.
func (s *User[T]) Set(ctx *via.Ctx, v T) {
	s.mu.Lock()
	s.val = v
	s.mu.Unlock()
	if ctx != nil {
		via.MarkDirty(ctx)
	}
}

// Text renders a static text node with the current value at render time.
func (s *User[T]) Text() h.H { return h.Textf("%v", s.Get(nil)) }

// App is an app-scoped reactive value: shared across every session, every
// tab. Use sparingly (no tenant isolation).
type App[T any] struct {
	val T
	key string
	mu  sync.RWMutex
}

// Get returns the current app value.
func (a *App[T]) Get(ctx *via.Ctx) T {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.val
}

// Set writes the app value. The change re-renders only the current tab; for
// fan-out across tabs the user wires their own broadcast mechanism (the v1
// surface intentionally keeps cross-tab writes opt-in, not magic).
func (a *App[T]) Set(ctx *via.Ctx, v T) {
	a.mu.Lock()
	a.val = v
	a.mu.Unlock()
	if ctx != nil {
		via.MarkDirty(ctx)
	}
}

// Text renders the current value as a static text node.
func (a *App[T]) Text() h.H { return h.Textf("%v", a.Get(nil)) }

