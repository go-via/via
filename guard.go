package via

import (
	"errors"
	"log"
	"net/http"

	"github.com/go-via/via/internal/hcore"
)

// Guard runs before OnInit on EVERY transport a mount answers — the page GET,
// a plain action, a live action over an open stream, and the SSE connect —
// unlike OnInit, which only runs on the first two and so cannot re-authorize
// a live action after the session it was opened under changes.
//
// The session it sees is writable (Session.Rotate, Session.Put), same as
// OnInit's. There is no header setter: a live action's "response" may be an
// SSE frame on a connection this call did not open (see Ctx.Request), so a
// JWT-refresh header belongs in an outer func(http.Handler) http.Handler
// wrapping the *Router. A Guard must also not consume req.Body — decodeSignals
// reads it after every guard has run.
//
// A non-nil error denies the request: ErrNotFound answers 404, ErrForbidden
// answers 403, anything else answers 500. A queued Ctx.Redirect denies by
// navigating instead. Either one stops the chain.
type Guard func(*Ctx) error

// Protect adds guards to one Mount, run after any router-wide WithGuard chain.
// A mount may only narrow a router's policy, never widen it, so there is no
// way to exempt a mount from a router-wide guard.
func Protect(g ...Guard) MountOption {
	return func(mc *mountConfig) { mc.guards = append(mc.guards, g...) }
}

// WithGuard adds guards that run on every mount of this Router, before any
// Protect the mount adds of its own. Applied once per Mount, never inside
// Router.ServeHTTP — a router-wide check there would also gate the datastar
// asset route and any sibling handler registered next to the Router.
func WithGuard(g ...Guard) Option {
	return func(c *config) { c.guards = append(c.guards, g...) }
}

// runGuards reports whether the caller may proceed; it has already answered w
// when it returns false.
//
// sse distinguishes the SSE connect from every other transport: a Redirect
// queued there would otherwise 303 the connect itself, and fetch follows a
// redirect by delivering the target page's HTML as the SSE body (see
// reconnect.go) — so a connect denial answers a plain 403 instead, which the
// client's existing reconnect error arm already turns into a banner.
func (m *mount) runGuards(w http.ResponseWriter, req *http.Request, mode actionMode, sse bool, base string) bool {
	if len(m.guards) == 0 {
		return true
	}
	ctx := newRootCtx(false, base, nil)
	ctx.req = req
	ctx.sessions = m.sessions
	ctx.sessW = w
	// Eager, like runOnInit: a nil session handle here would let each guard
	// (and OnInit right after it) resolve its own, minting and Set-Cookie-ing
	// independently instead of sharing the one this request settles on.
	ctx.Session()
	for _, g := range m.guards {
		if err := g(ctx); err != nil {
			noteErr(w, err)
			switch {
			case errors.Is(err, ErrNotFound):
				http.Error(w, "not found", http.StatusNotFound)
			case errors.Is(err, ErrForbidden):
				http.Error(w, "forbidden", http.StatusForbidden)
			default:
				log.Printf("via: guard failed: %q", err)
				http.Error(w, "guard failed", http.StatusInternalServerError)
			}
			return false
		}
		if ctx.redirect == "" {
			continue
		}
		if !hcore.SafeURL(ctx.redirect) {
			log.Printf("via: unsafe guard redirect %q dropped", ctx.redirect)
			http.Error(w, "guard failed", http.StatusInternalServerError)
			return false
		}
		if sse {
			http.Error(w, "forbidden", http.StatusForbidden)
			return false
		}
		respond(w, req, mode, ctx.redirect, nil, nil)
		return false
	}
	return true
}
