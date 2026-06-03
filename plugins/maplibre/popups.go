package maplibre

import (
	"fmt"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// popupConfig holds the MapLibre Popup constructor options a [PopupOption] sets.
type popupConfig struct {
	opts map[string]any
}

// PopupOption configures a popup shown with [Map.ShowPopup] / [Map.ShowPopupHTML].
type PopupOption func(*popupConfig)

// WithoutCloseButton hides the popup's × button (MapLibre shows it by default).
func WithoutCloseButton() PopupOption {
	return func(c *popupConfig) { c.opts["closeButton"] = false }
}

// WithoutCloseOnClick keeps the popup open when the user clicks the map
// (MapLibre closes it by default).
func WithoutCloseOnClick() PopupOption {
	return func(c *popupConfig) { c.opts["closeOnClick"] = false }
}

// PopupMaxWidth caps the popup width (any CSS length, e.g. "240px").
func PopupMaxWidth(s string) PopupOption {
	return func(c *popupConfig) { c.opts["maxWidth"] = s }
}

// PopupClass adds CSS classes to the popup container. Panics if any single arg
// contains whitespace — "a b" as one arg silently becomes two classes; pass
// them as separate args.
func PopupClass(parts ...string) PopupOption {
	for _, p := range parts {
		if strings.ContainsAny(p, " \t\n\r\f") {
			panic(fmt.Errorf("maplibre: PopupClass: class name %q must not contain whitespace (use separate args)", p))
		}
	}
	return func(c *popupConfig) { c.opts["className"] = strings.Join(parts, " ") }
}

// ShowPopup opens (or replaces) a keyed popup at at, with plain-text content
// rendered as a DOM text node — XSS-safe, so it's the right choice for
// user-supplied content. Re-using an id replaces the previous popup rather than
// stacking; [Map.ClosePopup] closes it. This is the "dialog on click" pattern:
// from an [OnFeatureClick] / [OnClick] handler, ShowPopup at e.LngLat() with
// details looked up on the server.
func (m *Map) ShowPopup(ctx *via.Ctx, id string, at LngLat, text string, opts ...PopupOption) {
	ctx.ExecScript(m.popupScript(id, at, "setText", text, opts))
}

// ShowPopupHTML is [Map.ShowPopup] with an [h.H] body, so you compose the popup
// with the same h.* builders as the rest of your view. Content built with h.T
// is escaped (safe for user data); h.Raw is injected unescaped — MapLibre does
// not sanitize it, so only use h.Raw with trusted markup.
func (m *Map) ShowPopupHTML(ctx *via.Ctx, id string, at LngLat, content h.H, opts ...PopupOption) {
	ctx.ExecScript(m.popupScript(id, at, "setHTML", renderH(content), opts))
}

// popupScript builds the self-invoking script that opens (or replaces) a keyed
// popup. setter is "setText" (safe) or "setHTML" (trusted). The popup registry
// is created lazily so the init registry literal stays untouched.
func (m *Map) popupScript(id string, at LngLat, setter, content string, opts []PopupOption) string {
	cfg := &popupConfig{opts: map[string]any{}}
	for _, o := range opts {
		o(cfg)
	}
	jid := mustJSON(id)
	var b strings.Builder
	fmt.Fprintf(&b, "var _e=window.__viaMaps&&window.__viaMaps[%d];if(!_e||!_e.m)return;", m.seq)
	b.WriteString("var _pp=_e.popups||(_e.popups={});")
	fmt.Fprintf(&b, "if(_pp[%s])_pp[%s].remove();", jid, jid)
	fmt.Fprintf(&b, "var _p=new maplibregl.Popup(%s).setLngLat(%s).%s(%s).addTo(_e.m);",
		mustJSON(cfg.opts), mustJSON(at.pair()), setter, mustJSON(content))
	fmt.Fprintf(&b, "_p.on('close',function(){delete _pp[%s]});", jid)
	fmt.Fprintf(&b, "_pp[%s]=_p;", jid)
	return "(function(){" + b.String() + "})();"
}

// ClosePopup closes the keyed popup. A no-op if no popup holds that id.
func (m *Map) ClosePopup(ctx *via.Ctx, id string) {
	jid := mustJSON(id)
	ctx.ExecScript(fmt.Sprintf(
		"(function(){var _e=window.__viaMaps&&window.__viaMaps[%d];var _pp=_e&&_e.popups;var _p=_pp&&_pp[%s];if(_p){_p.remove();delete _pp[%s]}})();",
		m.seq, jid, jid))
}
