// Package sessbridge lets the via/sess package reach the unexported
// session KV methods on via.Session without via exporting an untyped
// Load/Store/Delete surface. via sets the function vars at init; sess
// is the only consumer. The `any` here is internal plumbing — the
// public typed API lives in via/sess.
//
// A plain function-var bridge (rather than an interface) is used because
// via cannot import this package's types from sess's point of view
// without creating a via ↔ sessbridge type cycle.
package sessbridge

var (
	// Load reads the value stored under key on a *via.Session.
	Load func(s any, key string) (any, bool)
	// Store writes value under key on a *via.Session.
	Store func(s any, key string, value any)
	// Delete removes the value under key on a *via.Session.
	Delete func(s any, key string)
)
