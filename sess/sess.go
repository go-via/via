// Package sess provides typed, per-browser session storage for via apps.
//
// A session value is keyed by the Go type used to store it — one
// User{} per session, one ShoppingCart{} per session, and so on.
// Pair with [Rotate] after authentication state changes (login,
// logout, privilege elevation) to invalidate any captured pre-auth
// session id.
//
//	type User struct{ Email, Name string }
//
//	sess.Put(ctx, User{Email: "alice@example.com"})
//	u, ok := sess.Get[User](ctx)
//	sess.Clear[User](ctx)
//	sess.Rotate(ctx)
//
// Get and Clear also accept *via.CtxR (inside a render) and
// *http.Request, so middleware can check the session before any
// composition is rendered:
//
//	func requireAuth(w http.ResponseWriter, r *http.Request, next http.Handler) {
//	    if u, ok := sess.Get[User](r); !ok || u.Email == "" {
//	        http.Redirect(w, r, "/login", http.StatusSeeOther)
//	        return
//	    }
//	    next.ServeHTTP(w, r)
//	}
package sess

import (
	"net/http"
	"reflect"
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/internal/sessbridge"
)

// Source constrains where a session can be resolved from: a *via.Ctx
// (actions / handlers), a *via.CtxR (reads during a render), or an
// *http.Request (middleware, before any composition is rendered).
type Source interface {
	*via.Ctx | *via.CtxR | *http.Request
}

// session resolves src to its *via.Session. The type switch is
// exhaustive over [Source]; the constraint guarantees no other type can
// reach the default branch.
func session[S Source](src S) *via.Session {
	switch v := any(src).(type) {
	case *via.Ctx:
		return v.Session()
	case *via.CtxR:
		return v.Session()
	case *http.Request:
		return via.RequestSession(v)
	}
	return nil
}

// Put stores a typed value in the session bound to ctx, keyed by the
// type name. Use it to attach "the logged-in user" or any struct that
// is one-per-session. Marks the page dirty so the view re-renders.
//
//	type User struct{ Email, Name string }
//	sess.Put(ctx, User{Email: "alice@example.com"})
func Put[T any](ctx *via.Ctx, v T) {
	if ctx == nil {
		return
	}
	sessbridge.Store(ctx.Session(), typeKey[T](), v)
}

// Get reads the typed value stored with [Put], returning the zero
// value of T and false if nothing matches. src may be any [Source]: a
// *via.Ctx, a *via.CtxR (for reads during a render), or an
// *http.Request — the last form lets middleware check the session
// before any composition is rendered.
func Get[T any, S Source](src S) (T, bool) {
	var zero T
	raw, ok := sessbridge.Load(session(src), typeKey[T]())
	if !ok {
		return zero, false
	}
	t, ok := raw.(T)
	return t, ok
}

// Clear removes the value stored under T's key from the session. src
// may be any [Source] — the same kinds [Get] accepts, so a value read
// during a render can be cleared from the same render.
func Clear[T any, S Source](src S) {
	sessbridge.Delete(session(src), typeKey[T]())
}

// Rotate issues a fresh session id, copies the current session's data
// into it, and points the Ctx + the cookie on the in-flight response
// at the new session. Returns the new id, or "" if rotation could not
// be performed.
//
// Must be called from inside an action handler (a Writer must be live
// to set the new cookie).
func Rotate(ctx *via.Ctx) string { return ctx.Session().Rotate() }

// typeKeyCache memoises typeKey results so Get/Put/Clear hot paths
// avoid repeated string concatenation. Keyed by reflect.Type which is
// canonical and comparable.
var typeKeyCache sync.Map // map[reflect.Type]string

// typeKey returns a stable string key for a Go type used as a typed
// session value. We use the reflect type's full string ("pkg.Name")
// so distinct types in different packages don't collide.
func typeKey[T any]() string {
	var zero T
	rt := reflect.TypeOf(&zero).Elem()
	if v, ok := typeKeyCache.Load(rt); ok {
		return v.(string)
	}
	key := "type:" + rt.String()
	typeKeyCache.Store(rt, key)
	return key
}
