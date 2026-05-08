package h

import (
	g "maragu.dev/gomponents"
)

// retype converts a slice of via's H interface values into the
// gomponents.Node slice that gomponents element constructors expect.
// Empty input returns nil — variadic call sites accept that identically
// to a zero-length slice.
func retype(nodes []H) []g.Node {
	if len(nodes) == 0 {
		return nil
	}
	list := make([]g.Node, len(nodes))
	for i, node := range nodes {
		// (g.Node)(nil) on a nil interface yields (nil, false) — safe to
		// drop the explicit nil guard, the zero slice value covers it.
		list[i], _ = node.(g.Node)
	}
	return list
}
