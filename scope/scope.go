// Package scope holds non-tab state handles. Tab-scoped state already lives
// at via.State[T]; this package adds wider scopes:
//
//	type Profile struct {
//	    Theme scope.User[string]   // session-scoped: shared across tabs
//	    Hits  scope.App[int]       // app-scoped: shared across sessions
//	}
//
// Each scope is a distinct concrete type so that mismatching the scope at
// the call site is a compile error. Storage is provided by the via runtime:
// scope.User reads/writes through the session store; scope.App reads/writes
// through the app store. The handle itself holds only the wire key.
package scope

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// User is a session-scoped reactive value: shared across every tab opened
// from the same browser session, expires per via.WithSessionTTL.
type User[T any] struct {
	// WireKey is set by via.Mount from the field name (or the via:"…"
	// tag override). The runtime writes it once at registration and
	// the handle reads it on every Get/Set/Text.
	WireKey string
}

// Key returns the wire key (lowercase field name unless overridden by tag).
func (s *User[T]) Key() string { return s.WireKey }

// Get returns the current session value, or the zero value of T if unset.
func (s *User[T]) Get(ctx *via.Ctx) T {
	v, ok := via.SessionLoad(ctx, s.WireKey)
	if !ok {
		var zero T
		return zero
	}
	t, _ := v.(T)
	return t
}

// Set writes the session value and re-renders the current tab.
func (s *User[T]) Set(ctx *via.Ctx, v T) {
	via.SessionStore(ctx, s.WireKey, v)
}

// Text renders the current value as a static text node.
func (s *User[T]) Text(ctx *via.Ctx) h.H { return h.Textf("%v", s.Get(ctx)) }

// App is an app-scoped reactive value: shared across every session, every
// tab. Use sparingly (no tenant isolation).
type App[T any] struct {
	// WireKey is set by via.Mount from the field name (or the via:"…"
	// tag override).
	WireKey string
}

// Key returns the wire key.
func (a *App[T]) Key() string { return a.WireKey }

// Get returns the current app value, or the zero value of T if unset.
func (a *App[T]) Get(ctx *via.Ctx) T {
	v, ok := via.AppLoad(ctx, a.WireKey)
	if !ok {
		var zero T
		return zero
	}
	t, _ := v.(T)
	return t
}

// Set writes the app value and re-renders the current tab. Other tabs do
// not auto-update — they re-fetch on their next render or via the user's
// own broadcast mechanism.
func (a *App[T]) Set(ctx *via.Ctx, v T) {
	via.AppStore(ctx, a.WireKey, v)
}

// Text renders the current value as a static text node.
func (a *App[T]) Text(ctx *via.Ctx) h.H { return h.Textf("%v", a.Get(ctx)) }

