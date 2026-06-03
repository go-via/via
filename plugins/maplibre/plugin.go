package maplibre

import (
	"fmt"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

const (
	defaultVersion = "5.24.0"

	// dist/maplibre-gl.js is ALREADY the minified production build; there is
	// no .min.js. The CSP variant swaps the blob-URL web worker for an inline
	// one so a strict `worker-src` policy (no blob:) doesn't break the map.
	jsCDN    = "https://cdn.jsdelivr.net/npm/maplibre-gl@%s/dist/maplibre-gl.js"
	jsCSPCDN = "https://cdn.jsdelivr.net/npm/maplibre-gl@%s/dist/maplibre-gl-csp.js"
	cssCDN   = "https://cdn.jsdelivr.net/npm/maplibre-gl@%s/dist/maplibre-gl.css"
)

// registryJS declares the per-page map registry and a re-arming style-ready
// guard. isStyleLoaded() returns false transiently while a setStyle diff is
// in flight, so source/layer ops re-arm on 'styledata' rather than no-op or
// throw during the reload window.
const registryJS = `window.__viaMaps=window.__viaMaps||{};` +
	`window.__viaMapReady=window.__viaMapReady||function(m,fn){` +
	`if(m.isStyleLoaded()){fn()}else{m.once('styledata',function(){window.__viaMapReady(m,fn)})}};`

type pluginOptions struct {
	version   string
	jsSource  string
	cssSource string
	cspBuild  bool
}

// PluginOption configures the MapLibre plugin. Options are applied in
// argument order; each mutates the plugin in place.
type PluginOption func(*plugin)

// WithVersion pins the MapLibre GL JS CDN version for both the JS and the
// CSS. Pin a v5 release: v6 is ESM-only and drops the `maplibregl` global a
// `<script src>` include relies on. Panics on empty string.
func WithVersion(version string) PluginOption {
	if version == "" {
		panic("maplibre: WithVersion: version cannot be empty")
	}
	return func(p *plugin) { p.opts.version = version }
}

// WithSource overrides the maplibre-gl.js URL entirely — for self-hosting
// (offline / air-gapped / strict CSP), pinning a custom build, or an internal
// mirror. Takes precedence over WithVersion and WithCSPBuild for the JS; the
// CSS still follows WithVersion unless WithStylesheet is also set. Panics on
// empty string, since a silent CDN fallback would defeat opting in.
func WithSource(url string) PluginOption {
	if url == "" {
		panic("maplibre: WithSource: url cannot be empty")
	}
	return func(p *plugin) { p.opts.jsSource = url }
}

// WithStylesheet overrides the maplibre-gl.css URL entirely. The CSS is
// required — without it popups, markers, and controls render unstyled and
// mispositioned. Takes precedence over WithVersion for the CSS. Panics on
// empty string.
func WithStylesheet(url string) PluginOption {
	if url == "" {
		panic("maplibre: WithStylesheet: url cannot be empty")
	}
	return func(p *plugin) { p.opts.cssSource = url }
}

// WithCSPBuild loads the CSP-safe bundle (maplibre-gl-csp.js) instead of the
// default build. The default build spawns its WebGL worker from a blob: URL,
// which a strict `worker-src` policy blocks; the CSP build inlines the worker
// instead. Has no effect when WithSource sets an explicit JS URL.
func WithCSPBuild() PluginOption {
	return func(p *plugin) { p.opts.cspBuild = true }
}

// Plugin integrates MapLibre GL JS. By default it loads the JS + CSS from
// jsDelivr at the pinned default version and exposes the `maplibregl` global.
// Hold a [Map] on the page, mount it in View, and drive it from actions or a
// [via.Stream] ticker over SSE.
func Plugin(opts ...PluginOption) via.Plugin {
	p := &plugin{opts: pluginOptions{version: defaultVersion}}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

type plugin struct {
	opts pluginOptions
}

func (p *plugin) Register(v *via.App) {
	js := p.opts.jsSource
	if js == "" {
		tmpl := jsCDN
		if p.opts.cspBuild {
			tmpl = jsCSPCDN
		}
		js = fmt.Sprintf(tmpl, p.opts.version)
	}
	css := p.opts.cssSource
	if css == "" {
		css = fmt.Sprintf(cssCDN, p.opts.version)
	}

	// Synchronous, no defer/async: maplibregl must be defined before any
	// inline map-init script in the body runs.
	v.AppendToHead(h.Link(h.Rel("stylesheet"), h.Href(css)))
	v.AppendToHead(h.Script(h.Src(js)))
	v.AppendToHead(h.Script(h.Raw(registryJS)))
}
