package shell

import "github.com/go-via/via"

// EnsureSession mints a session id for a visitor who has stored nothing: the
// id is "" until something is put, and the rate limits and the ping demo's
// fan-out key on it. It stores null to leave the session's one slot free —
// demos/auth.go reads a stored user with no name as signed out.
//
// Only a page carrying a demo that keys on the session calls it. It sets a
// cookie, so the pages that need no session (/signals, /islands, /reference)
// deliberately leave the visitor without one.
func EnsureSession(ctx *via.Ctx) {
	if ctx.Session().ID() == "" {
		ctx.Session().Put(nil)
	}
}
