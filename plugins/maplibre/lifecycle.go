package maplibre

import (
	"fmt"
	"strings"

	"github.com/go-via/via"
)

// SetStyle swaps the basemap style to a new Style Spec URL — e.g. toggling a
// streets/satellite/dark basemap. Sources and layers you added are reapplied
// by MapLibre's style diff where possible.
func (m *Map) SetStyle(ctx *via.Ctx, url string) {
	ctx.ExecScript(fmt.Sprintf("%s?.setStyle(%s)", m.mapRef(), mustJSON(url)))
}

// Resize tells the map to recompute its size. The mounted ResizeObserver
// already handles container box changes; call this for CSS-driven changes
// that don't alter the box (a sibling animating, a panel toggling).
func (m *Map) Resize(ctx *via.Ctx) {
	ctx.ExecScript(m.mapRef() + "?.resize()")
}

// Dispose tears the map down — frees the WebGL context, web workers, and DOM
// listeners, disconnects the ResizeObserver, and drops the registry slot.
// Call when the container is about to leave the DOM, or the instance leaks
// for the page's lifetime. Safe to call more than once.
func (m *Map) Dispose(ctx *via.Ctx) {
	ctx.ExecScript(fmt.Sprintf(
		"(function(){var _e=window.__viaMaps&&window.__viaMaps[%d];if(_e){if(_e.ro)_e.ro.disconnect();if(_e.m)_e.m.remove();delete window.__viaMaps[%d]}})()",
		m.seq, m.seq))
}

// Call is the escape hatch for any MapLibre Map method the typed helpers don't
// cover — e.g. Call(ctx, "setMaxZoom", 18) or Call(ctx, "panBy", []float64{100, 0}).
// Each arg is JSON-encoded as a positional argument. Returns an error only if
// an arg can't be marshalled.
func (m *Map) Call(ctx *via.Ctx, method string, args ...any) error {
	parts := make([]string, len(args))
	for i, a := range args {
		s, err := marshal(a)
		if err != nil {
			return err
		}
		parts[i] = s
	}
	ctx.ExecScript(fmt.Sprintf("%s?.%s(%s)", m.mapRef(), method, strings.Join(parts, ",")))
	return nil
}
