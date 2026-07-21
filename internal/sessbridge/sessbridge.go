// Package sessbridge lets the via/sess subpackage reach the unexported session
// KV methods on *via.Session without via exporting an untyped Load/Store/Delete
// surface on its public API. via sets the function vars in an init; sess is the
// only consumer. A plain function-var bridge (not an interface) avoids a
// via ↔ sessbridge type cycle. Keys are an opaque any — the public typed API,
// and the type-as-key scheme, live in via/sess.
package sessbridge

var (
	// Load reads the value stored under key on a *via.Session.
	Load func(s any, key any) (any, bool)
	// Store writes value under key on a *via.Session.
	Store func(s any, key any, value any)
	// Delete removes the value under key on a *via.Session.
	Delete func(s any, key any)
	// Rotate re-issues the session id on a *via.Session (fixation defense).
	Rotate func(s any) string
)
