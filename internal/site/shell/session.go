package shell

import "github.com/go-via/via"

// EnsureSession mints a session id for a visitor who has stored nothing: the
// id is "" until something is put, and the rate limits and the ping demo's
// fan-out both key on it. The session holds one value, so it stores null to
// leave that slot free — demos/auth.go reads a stored user with no name as
// signed out.
func EnsureSession(ctx *via.Ctx) {
	if ctx.Session().ID() == "" {
		ctx.Session().Put(nil)
	}
}
