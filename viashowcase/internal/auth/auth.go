// Package auth provides bcrypt password hashing plus session-backed
// current-user helpers and a route-guarding middleware.
package auth

import (
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/sess"
	"golang.org/x/crypto/bcrypt"
)

// Hash returns a bcrypt hash of pw.
func Hash(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

// Verify reports whether pw matches the bcrypt hash.
func Verify(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// SessionUser is the logged-in identity stored in the session.
type SessionUser struct{ ID, Email, Display string }

// Login stores u in the session and rotates the session id.
func Login(ctx *via.Ctx, u SessionUser) {
	sess.Put(ctx, u)
	sess.Rotate(ctx)
}

// SetCurrent updates the logged-in identity in place (no rotation) — for profile
// edits like a display-name change, so the nav reflects it immediately.
func SetCurrent(ctx *via.Ctx, u SessionUser) { sess.Put(ctx, u) }

// Logout clears the user from the session and rotates the session id.
func Logout(ctx *via.Ctx) {
	sess.Clear[SessionUser](ctx)
	sess.Rotate(ctx)
}

// Current resolves the logged-in user from any sess.Source
// (*via.Ctx, *via.CtxR, *http.Request).
func Current[S sess.Source](src S) (SessionUser, bool) {
	return sess.Get[SessionUser](src)
}

// Require returns middleware that 302-redirects to /login when the
// request session has no SessionUser.
func Require() via.Middleware {
	return func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if u, ok := Current(r); !ok || u.ID == "" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	}
}
