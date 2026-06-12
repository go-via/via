package maplibre

//go:generate sh refresh_assets.sh

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// pinnedVersion is the vendored MapLibre GL JS release; refresh_assets.sh
// must be re-run (and the embedded files re-vendored) whenever it
// changes. Pin a v5 release: v6 is ESM-only and drops the `maplibregl`
// global a <script src> include relies on.
const pinnedVersion = "5.24.0"

// registryJS declares the per-page map registry and a re-arming style-ready
// guard. isStyleLoaded() returns false transiently while a setStyle diff is
// in flight, so source/layer ops re-arm on 'styledata' rather than no-op or
// throw during the reload window.
const registryJS = `window.__viaMaps=window.__viaMaps||{};` +
	`window.__viaMapReady=window.__viaMapReady||function(m,fn){` +
	`if(m.isStyleLoaded()){fn()}else{m.once('styledata',function(){window.__viaMapReady(m,fn)})}};`

type pluginOptions struct {
	jsSource        string
	cssSource       string
	cdnJS           string
	cdnJSIntegrity  string
	cdnCSS          string
	cdnCSSIntegrity string
	cspBuild        bool
}

// PluginOption configures the MapLibre plugin. Options are applied in
// argument order; each mutates the plugin in place. Conflicting or
// duplicate options panic at registration.
type PluginOption func(*plugin)

// WithVersion exists to catch stale version pins: the MapLibre build is
// embedded at pinnedVersion, so restating that version is a no-op and
// any other version panics — there is no embedded asset (and no SRI
// hash) to back it. To run a different version, pass WithCDN /
// WithCDNStylesheet with URLs and integrity hashes for that exact
// build, or self-host it via WithSource / WithStylesheet.
func WithVersion(version string) PluginOption {
	if version == "" {
		panic("maplibre: WithVersion: version cannot be empty")
	}
	return func(p *plugin) {
		if version != pinnedVersion {
			panic("maplibre: WithVersion(" + version + "): the embedded MapLibre build is pinned at " +
				pinnedVersion + " — a bare version bump has no asset and no integrity hash; " +
				"use WithCDN(url, integrity) or WithSource(path)")
		}
	}
}

// WithSource overrides the maplibre-gl.js URL with a same-origin path —
// for self-hosting a custom build or an internal mirror. Only the JS is
// overridden; the CSS stays embedded unless WithStylesheet is also set.
// Panics on an empty string, on a cross-origin URL (use WithCDN, which
// requires an integrity hash), or when combined with WithCDN or
// WithCSPBuild.
func WithSource(url string) PluginOption {
	mustSameOriginURL("maplibre: WithSource", url)
	return func(p *plugin) {
		if p.opts.cdnJS != "" {
			panic("maplibre: WithSource conflicts with WithCDN — pick one JS source")
		}
		if p.opts.cspBuild {
			panic("maplibre: WithSource conflicts with WithCSPBuild — the source URL already names a bundle")
		}
		if p.opts.jsSource != "" {
			panic("maplibre: WithSource set twice — conflicting JS sources")
		}
		p.opts.jsSource = url
	}
}

// WithStylesheet overrides the maplibre-gl.css URL with a same-origin
// path. The CSS is required — without it popups, markers, and controls
// render unstyled and mispositioned. Panics on an empty string, on a
// cross-origin URL (use WithCDNStylesheet), or when combined with
// WithCDNStylesheet.
func WithStylesheet(url string) PluginOption {
	mustSameOriginURL("maplibre: WithStylesheet", url)
	return func(p *plugin) {
		if p.opts.cdnCSS != "" {
			panic("maplibre: WithStylesheet conflicts with WithCDNStylesheet — pick one CSS source")
		}
		if p.opts.cssSource != "" {
			panic("maplibre: WithStylesheet set twice — conflicting CSS sources")
		}
		p.opts.cssSource = url
	}
}

// WithCDN opts in to loading maplibre-gl.js from a CDN instead of the
// embedded build. integrity is mandatory and must be a well-formed SRI
// value (sha256-/sha384-/sha512- followed by base64 of the digest) for
// the exact body served at url — the emitted <script> tag carries it
// plus crossorigin="anonymous", so a tampered CDN response is refused
// by the browser. Changing the URL (e.g. a version bump) requires
// supplying the new build's hash. Panics on a malformed url or
// integrity, or when combined with WithSource or WithCSPBuild.
func WithCDN(url, integrity string) PluginOption {
	mustCrossOriginURL("maplibre: WithCDN", url)
	mustValidIntegrity("maplibre: WithCDN", integrity)
	return func(p *plugin) {
		if p.opts.jsSource != "" {
			panic("maplibre: WithCDN conflicts with WithSource — pick one JS source")
		}
		if p.opts.cspBuild {
			panic("maplibre: WithCDN conflicts with WithCSPBuild — the CDN URL already names a bundle")
		}
		if p.opts.cdnJS != "" {
			panic("maplibre: WithCDN set twice — conflicting JS sources")
		}
		p.opts.cdnJS = url
		p.opts.cdnJSIntegrity = integrity
	}
}

// WithCDNStylesheet opts in to loading maplibre-gl.css from a CDN with
// the same mandatory-SRI contract as WithCDN: the emitted <link> tag
// carries the integrity hash plus crossorigin="anonymous". Panics on a
// malformed url or integrity, or when combined with WithStylesheet.
func WithCDNStylesheet(url, integrity string) PluginOption {
	mustCrossOriginURL("maplibre: WithCDNStylesheet", url)
	mustValidIntegrity("maplibre: WithCDNStylesheet", integrity)
	return func(p *plugin) {
		if p.opts.cssSource != "" {
			panic("maplibre: WithCDNStylesheet conflicts with WithStylesheet — pick one CSS source")
		}
		if p.opts.cdnCSS != "" {
			panic("maplibre: WithCDNStylesheet set twice — conflicting CSS sources")
		}
		p.opts.cdnCSS = url
		p.opts.cdnCSSIntegrity = integrity
	}
}

// WithCSPBuild serves the embedded CSP-safe bundle (maplibre-gl-csp.js)
// instead of the default build, and points maplibregl.workerUrl at the
// embedded companion worker. The default build spawns its WebGL worker
// from a blob: URL, which a strict `worker-src` policy blocks; the CSP
// build loads the worker from a same-origin URL instead. Panics when
// combined with WithSource or WithCDN, which name a bundle themselves.
func WithCSPBuild() PluginOption {
	return func(p *plugin) {
		if p.opts.jsSource != "" {
			panic("maplibre: WithCSPBuild conflicts with WithSource — the source URL already names a bundle")
		}
		if p.opts.cdnJS != "" {
			panic("maplibre: WithCSPBuild conflicts with WithCDN — the CDN URL already names a bundle")
		}
		p.opts.cspBuild = true
	}
}

// Plugin integrates MapLibre GL JS. By default it serves the embedded
// JS + CSS (pinned at pinnedVersion) from content-hashed
// /via/assets/maplibre/ paths and exposes the `maplibregl` global —
// registration does no network I/O and pages reference no third-party
// origin. Use the WithCDN options to opt in to CDN delivery (SRI
// mandatory). Hold a [Map] on the page, mount it in View, and drive it
// from actions or a [via.Stream] ticker over SSE.
func Plugin(opts ...PluginOption) via.Plugin {
	p := &plugin{}
	for _, opt := range opts {
		opt(p)
	}
	p.js = newAsset("maplibre-gl.js", "text/javascript")
	p.cspJS = newAsset("maplibre-gl-csp.js", "text/javascript")
	p.cspWorker = newAsset("maplibre-gl-csp-worker.js", "text/javascript")
	p.css = newAsset("maplibre-gl.css", "text/css")
	p.assetsByName = map[string]*asset{
		p.js.name:        p.js,
		p.cspJS.name:     p.cspJS,
		p.cspWorker.name: p.cspWorker,
		p.css.name:       p.css,
	}
	return p
}

type plugin struct {
	opts         pluginOptions
	js           *asset
	cspJS        *asset
	cspWorker    *asset
	css          *asset
	assetsByName map[string]*asset
}

func (p *plugin) Register(v *via.App) {
	v.HandleFunc("GET "+assetPathPrefix, p.serveAssets)

	switch {
	case p.opts.cdnCSS != "":
		v.AppendToHead(h.Link(
			h.Rel("stylesheet"),
			h.Href(p.opts.cdnCSS),
			h.Attr("integrity", p.opts.cdnCSSIntegrity),
			h.Attr("crossorigin", "anonymous"),
		))
	case p.opts.cssSource != "":
		v.AppendToHead(h.Link(h.Rel("stylesheet"), h.Href(p.opts.cssSource)))
	default:
		v.AppendToHead(h.Link(h.Rel("stylesheet"), h.Href(p.css.path())))
	}

	// Synchronous, no defer/async: maplibregl must be defined before any
	// inline map-init script in the body runs.
	switch {
	case p.opts.cdnJS != "":
		v.AppendToHead(h.Script(
			h.Src(p.opts.cdnJS),
			h.Attr("integrity", p.opts.cdnJSIntegrity),
			h.Attr("crossorigin", "anonymous"),
		))
	case p.opts.jsSource != "":
		v.AppendToHead(h.Script(h.Src(p.opts.jsSource)))
	case p.opts.cspBuild:
		v.AppendToHead(h.Script(h.Src(p.cspJS.path())))
		// The CSP bundle ships with an empty worker URL by design; maps
		// can't boot until it points at the companion worker script.
		v.AppendToHead(h.Script(h.Raw(
			"maplibregl.workerUrl='" + p.cspWorker.path() + "';")))
	default:
		v.AppendToHead(h.Script(h.Src(p.js.path())))
	}

	v.AppendToHead(h.Script(h.Raw(registryJS)))
}
