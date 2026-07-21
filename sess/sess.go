// Package sess provides typed, per-browser session storage for via apps.
//
// A session value is keyed by the Go type used to store it — one User{} per
// session, one ShoppingCart{} per session, and so on. Pair with [Rotate] after
// authentication state changes (login, logout, privilege elevation) to
// invalidate any captured pre-auth session id.
//
//	type User struct{ Email, Name string }
//
//	sess.Put(ctx, User{Email: "alice@example.com"})
//	u, ok := sess.Get[User](ctx)
//	sess.Clear[User](ctx)
//
// Sessions are opt-in (via.WithSessionKey / WithSessionTTL / WithSessionCookieName)
// and lazy: the cookie is issued only on the first Put, and only where a response
// is open — a stateless action or OnConnect. A live action runs after its 204, so
// it can mutate an already-established session but cannot create one; establish
// the session in OnConnect or a stateless action first.
//
// The store is in-memory and single-pod (the 1.0 scope). Expiry is enforced
// lazily on access: a session idle past its TTL stops resolving, but a session
// that is never accessed again is not actively swept — acceptable because a
// session is only created on a write (typically login), so growth tracks real
// authenticated sessions, not anonymous traffic. A background sweep / durable
// or cross-pod store is deferred to the backplane work.
package sess

import (
	"github.com/go-via/via"
	"github.com/go-via/via/internal/sessbridge"
)

// typeKey returns a stable, comparable key unique to T — a typed nil pointer,
// so distinct types never collide and the same type always matches. No reflect:
// (*T)(nil) boxed in an interface carries T's identity for free.
func typeKey[T any]() any { return (*T)(nil) }

// Put stores a typed value in ctx's session, keyed by its type — use it for the
// one-per-session value like the logged-in user. The first Put issues the
// session cookie; see the package doc for the live-action caveat.
func Put[T any](ctx *via.Ctx, v T) {
	if ctx == nil {
		return
	}
	sessbridge.Store(ctx.Session(), typeKey[T](), v)
}

// Get reads the value stored with [Put] for type T, returning the zero value and
// false when nothing is stored (including on an app that hasn't enabled sessions).
func Get[T any](ctx *via.Ctx) (T, bool) {
	var zero T
	if ctx == nil {
		return zero, false
	}
	raw, ok := sessbridge.Load(ctx.Session(), typeKey[T]())
	if !ok {
		return zero, false
	}
	v, ok := raw.(T)
	return v, ok
}

// Clear removes the value stored under T's key — e.g. a logout dropping the
// session-held user.
func Clear[T any](ctx *via.Ctx) {
	if ctx == nil {
		return
	}
	sessbridge.Delete(ctx.Session(), typeKey[T]())
}

// Rotate issues a fresh session id (carrying the data over) and invalidates the
// old one — call it right after an auth state change to defend against session
// fixation. Returns the new id, or "" if no response is open to carry the new
// cookie (a live action); rotate from a stateless action or OnConnect.
func Rotate(ctx *via.Ctx) string {
	if ctx == nil {
		return ""
	}
	return sessbridge.Rotate(ctx.Session())
}
