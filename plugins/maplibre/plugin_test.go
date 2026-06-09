package maplibre_test

import (
	"io"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/maplibre"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type maplibrePage struct{}

func (p *maplibrePage) View(ctx *via.CtxR) h.H { return h.Div() }

// servePage boots a one-page app with the given plugin options and returns
// the rendered document HTML.
func servePage(t *testing.T, opts ...maplibre.PluginOption) string {
	t.Helper()

	app := via.New(
		via.WithPlugins(maplibre.Plugin(opts...)),
	)
	server := vt.Serve(t, app)
	via.Mount[maplibrePage](app, "/")
	t.Cleanup(server.Close)

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestPlugin_injectsJSAndCSSAtDefaultVersion(t *testing.T) {
	t.Parallel()
	html := servePage(t)

	assert.Contains(t, html, "maplibre-gl@5.24.0/dist/maplibre-gl.js",
		"plugin must attach the MapLibre JS at the pinned default version")
	assert.Contains(t, html, "maplibre-gl@5.24.0/dist/maplibre-gl.css",
		"the CSS is required — popups/markers/controls break without it")
	assert.Contains(t, html, `rel="stylesheet"`,
		"the CSS must be a stylesheet link")
}

func TestPlugin_usesPlainNotMinJSBundle(t *testing.T) {
	t.Parallel()
	// dist/maplibre-gl.js IS the minified build; a .min.js path 404s.
	html := servePage(t)
	assert.NotContains(t, html, "maplibre-gl.min.js",
		"there is no .min.js bundle in dist — that path would 404")
}

func TestPlugin_WithVersion_pinsBothJSAndCSS(t *testing.T) {
	t.Parallel()
	html := servePage(t, maplibre.WithVersion("5.1.0"))

	assert.Contains(t, html, "maplibre-gl@5.1.0/dist/maplibre-gl.js")
	assert.Contains(t, html, "maplibre-gl@5.1.0/dist/maplibre-gl.css")
	assert.NotContains(t, html, "5.24.0",
		"the default version must not leak through when overridden")
}

func TestPlugin_WithSource_overridesJSURLOnly(t *testing.T) {
	t.Parallel()
	html := servePage(t, maplibre.WithSource("/static/maplibre.js"))

	assert.Contains(t, html, `src="/static/maplibre.js"`,
		"WithSource must drop in the self-hosted JS URL")
	assert.NotContains(t, html, "cdn.jsdelivr.net/npm/maplibre-gl@5.24.0/dist/maplibre-gl.js",
		"WithSource must replace the CDN JS, not append alongside it")
	assert.Contains(t, html, "maplibre-gl@5.24.0/dist/maplibre-gl.css",
		"WithSource overrides only the JS; the CSS stays on the default CDN")
}

func TestPlugin_WithStylesheet_overridesCSSURLOnly(t *testing.T) {
	t.Parallel()
	html := servePage(t, maplibre.WithStylesheet("/static/maplibre.css"))

	assert.Contains(t, html, `href="/static/maplibre.css"`,
		"WithStylesheet must drop in the self-hosted CSS URL")
	assert.Contains(t, html, "maplibre-gl@5.24.0/dist/maplibre-gl.js",
		"WithStylesheet overrides only the CSS; the JS stays on the default CDN")
}

func TestPlugin_WithCSPBuild_usesCSPBundle(t *testing.T) {
	t.Parallel()
	html := servePage(t, maplibre.WithCSPBuild())

	assert.Contains(t, html, "maplibre-gl-csp.js",
		"WithCSPBuild must load the CSP-safe bundle for strict worker-src policies")
	assert.NotContains(t, html, "/dist/maplibre-gl.js",
		"the blob-worker default bundle must not also load under WithCSPBuild")
}

func TestPlugin_registersReadyHelperAndRegistry(t *testing.T) {
	t.Parallel()
	html := servePage(t)

	assert.Contains(t, html, "__viaMaps",
		"the plugin must declare the map registry namespace")
	assert.Contains(t, html, "isStyleLoaded",
		"the plugin must define the style-ready guard")
	assert.Contains(t, html, "styledata",
		"the guard must re-arm on 'styledata' rather than no-op when the style isn't loaded yet")
}

func TestPlugin_panicsOnEmptyStringOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func()
	}{
		{"WithVersion", func() { maplibre.WithVersion("") }},
		{"WithSource", func() { maplibre.WithSource("") }},
		{"WithStylesheet", func() { maplibre.WithStylesheet("") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, tt.call,
				"an empty URL/version produces a broken include and must be rejected at the option boundary")
		})
	}
}
