package echarts

import (
	"fmt"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// CDN configuration defaults
const (
	defaultVersion = "6.0.0"
	cdnBase        = "https://cdn.jsdelivr.net/npm/echarts@%s/dist/echarts.min.js"
)

type chartOptions struct {
	version string
	source  string
}

// PluginOption configures the Echarts plugin. Each option mutates the
// plugin in place; Plugin applies them in argument order.
type PluginOption func(*plugin)

// WithVersion sets the ECharts CDN version. Panics on empty string —
// an empty version produces a malformed CDN URL that fails to load
// rather than gracefully degrading.
func WithVersion(version string) PluginOption {
	if version == "" {
		panic("echarts: WithVersion: version cannot be empty")
	}
	return func(p *plugin) { p.opts.version = version }
}

// WithSource overrides the echarts.min.js URL entirely — useful for
// self-hosting (offline / air-gapped / strict CSP), pinning a custom
// build, or pointing at an internal mirror. When set, WithVersion has
// no effect because the full URL is taken from this option. Panics on
// empty string since silently falling back to the CDN would defeat the
// caller's intent in opting into a custom source.
func WithSource(url string) PluginOption {
	if url == "" {
		panic("echarts: WithSource: url cannot be empty")
	}
	return func(p *plugin) { p.opts.source = url }
}

// Plugin creates a new Echarts plugin. By default it loads echarts
// from the jsDelivr CDN at the version pinned by defaultVersion. Use
// WithVersion to pin a different CDN version, or WithSource to point
// at a self-hosted build.
func Plugin(opts ...PluginOption) via.Plugin {
	p := &plugin{opts: chartOptions{version: defaultVersion}}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

type plugin struct {
	opts chartOptions
}

func (p *plugin) Register(v *via.App) {
	src := p.opts.source
	if src == "" {
		src = fmt.Sprintf(cdnBase, p.opts.version)
	}
	v.AppendToHead(h.Script(h.Src(src)))
}
