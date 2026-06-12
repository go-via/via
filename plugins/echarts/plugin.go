package echarts

//go:generate sh refresh_assets.sh

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// pinnedVersion is the vendored ECharts release; refresh_assets.sh must
// be re-run (and the embedded file re-vendored) whenever it changes.
const pinnedVersion = "6.0.0"

type chartOptions struct {
	source       string
	cdnURL       string
	cdnIntegrity string
}

// PluginOption configures the Echarts plugin. Each option mutates the
// plugin in place; Plugin applies them in argument order. Conflicting
// or duplicate options panic at registration.
type PluginOption func(*plugin)

// WithVersion exists to catch stale version pins: the echarts build is
// embedded at pinnedVersion, so restating that version is a no-op and
// any other version panics — there is no embedded asset (and no SRI
// hash) to back it. To run a different version, pass WithCDN with a
// URL and an integrity hash for that exact build, or self-host it via
// WithSource.
func WithVersion(version string) PluginOption {
	if version == "" {
		panic("echarts: WithVersion: version cannot be empty")
	}
	return func(p *plugin) {
		if version != pinnedVersion {
			panic("echarts: WithVersion(" + version + "): the embedded echarts build is pinned at " +
				pinnedVersion + " — a bare version bump has no asset and no integrity hash; " +
				"use WithCDN(url, integrity) or WithSource(path)")
		}
	}
}

// WithSource overrides the echarts.min.js URL with a same-origin path —
// for self-hosting a custom build or an internal mirror. Panics on an
// empty string, on a cross-origin URL (use WithCDN, which requires an
// integrity hash), or when combined with WithCDN.
func WithSource(url string) PluginOption {
	mustSameOriginURL("echarts: WithSource", url)
	return func(p *plugin) {
		if p.opts.cdnURL != "" {
			panic("echarts: WithSource conflicts with WithCDN — pick one script source")
		}
		if p.opts.source != "" {
			panic("echarts: WithSource set twice — conflicting script sources")
		}
		p.opts.source = url
	}
}

// WithCDN opts in to loading echarts.min.js from a CDN instead of the
// embedded build. integrity is mandatory and must be a well-formed SRI
// value (sha256-/sha384-/sha512- followed by base64 of the digest) for
// the exact body served at url — the emitted <script> tag carries it
// plus crossorigin="anonymous", so a tampered CDN response is refused
// by the browser. Changing the URL (e.g. a version bump) requires
// supplying the new build's hash; there is no way to opt out of SRI.
// Panics on a malformed url or integrity, or when combined with
// WithSource.
func WithCDN(url, integrity string) PluginOption {
	mustCrossOriginURL("echarts: WithCDN", url)
	mustValidIntegrity("echarts: WithCDN", integrity)
	return func(p *plugin) {
		if p.opts.source != "" {
			panic("echarts: WithCDN conflicts with WithSource — pick one script source")
		}
		if p.opts.cdnURL != "" {
			panic("echarts: WithCDN set twice — conflicting script sources")
		}
		p.opts.cdnURL = url
		p.opts.cdnIntegrity = integrity
	}
}

// Plugin creates a new Echarts plugin. By default it serves the
// embedded echarts build (pinned at pinnedVersion) from a
// content-hashed /via/assets/echarts/ path — registration does no
// network I/O and pages reference no third-party origin. Use WithCDN
// to opt in to CDN delivery (SRI mandatory) or WithSource to self-host.
func Plugin(opts ...PluginOption) via.Plugin {
	p := &plugin{js: newAsset("echarts.min.js", "text/javascript", echartsJS)}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

type plugin struct {
	opts chartOptions
	js   *asset
}

func (p *plugin) Register(v *via.App) {
	v.HandleFunc("GET "+assetPathPrefix, p.serveAssets)

	switch {
	case p.opts.cdnURL != "":
		v.AppendToHead(h.Script(
			h.Src(p.opts.cdnURL),
			h.Attr("integrity", p.opts.cdnIntegrity),
			h.Attr("crossorigin", "anonymous"),
		))
	case p.opts.source != "":
		v.AppendToHead(h.Script(h.Src(p.opts.source)))
	default:
		v.AppendToHead(h.Script(h.Src(p.js.path())))
	}
}
