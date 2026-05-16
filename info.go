package via

import (
	"cmp"
	"slices"
)

// CompositionInfo is one entry in App.Compositions().
type CompositionInfo struct {
	Type  string // type name, e.g. "via_test.Counter"
	Route string // mounted pattern
}

// Compositions returns a sorted snapshot of the names of every typed
// Composition mounted on this app, paired with its route. Useful for
// boot logging or status pages:
//
//	for _, c := range app.Compositions() {
//	    log.Printf("mounted %-30s at %s", c.Type, c.Route)
//	}
func (a *App) Compositions() []CompositionInfo {
	a.descsMu.RLock()
	out := make([]CompositionInfo, 0, len(a.descs))
	for _, d := range a.descs {
		out = append(out, CompositionInfo{
			Type:  d.typ.String(),
			Route: d.route,
		})
	}
	a.descsMu.RUnlock()
	slices.SortFunc(out, func(a, b CompositionInfo) int { return cmp.Compare(a.Route, b.Route) })
	return out
}

// RouteInfo is one entry in App.Routes().
type RouteInfo struct {
	Pattern      string // method-and-pattern, e.g. "GET /counter/{id}"
	RegisteredBy string // who claimed it: "Mount[Counter]", "HandleFunc", …
}

// Routes returns a sorted snapshot of every method+pattern registered on
// this app, paired with the registrar tag (Mount[T], HandleFunc,
// Group(prefix).Handle, …). Useful for `app.Routes()` debugging and for
// surfacing registered surface area at boot.
func (a *App) Routes() []RouteInfo {
	a.routesMu.Lock()
	out := make([]RouteInfo, 0, len(a.routes))
	for pattern, tag := range a.routes {
		out = append(out, RouteInfo{Pattern: pattern, RegisteredBy: tag})
	}
	a.routesMu.Unlock()
	slices.SortFunc(out, func(a, b RouteInfo) int { return cmp.Compare(a.Pattern, b.Pattern) })
	return out
}

// LiveTabs returns the number of currently-registered tab contexts.
// Useful for ops endpoints (/healthz, /metrics) that want to surface
// concurrency without scraping internal state. The number is a
// snapshot — it may have changed by the time the caller reads the
// return value.
func (a *App) LiveTabs() int {
	a.contextRegistryMu.RLock()
	defer a.contextRegistryMu.RUnlock()
	return len(a.contextRegistry)
}
