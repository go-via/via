package maplibre_test

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
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
	html, _ := servePageOn(t, opts...)
	return html
}

func servePageOn(t *testing.T, opts ...maplibre.PluginOption) (string, *httptest.Server) {
	t.Helper()

	app := via.New(
		via.WithPlugins(maplibre.Plugin(opts...)),
	)
	server := vt.Serve(t, app)
	via.Mount[maplibrePage](app, "/")

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body), server
}

// sriFor builds a syntactically valid sha384 SRI value; only the grammar
// is validated at registration.
func sriFor() string {
	return "sha384-" + base64.StdEncoding.EncodeToString(make([]byte, 48))
}

var (
	hashedJS     = regexp.MustCompile(`/via/assets/maplibre/[0-9a-f]+/maplibre-gl\.js`)
	hashedCSPJS  = regexp.MustCompile(`/via/assets/maplibre/[0-9a-f]+/maplibre-gl-csp\.js`)
	hashedWorker = regexp.MustCompile(`/via/assets/maplibre/[0-9a-f]+/maplibre-gl-csp-worker\.js`)
	hashedCSS    = regexp.MustCompile(`/via/assets/maplibre/[0-9a-f]+/maplibre-gl\.css`)
)

type refusingTransport struct{ t *testing.T }

func (rt refusingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.t.Errorf("plugin registration performed network I/O: %s %s", r.Method, r.URL)
	return nil, http.ErrUseLastResponse
}

func TestPlugin_registersWithoutNetworkAccess(t *testing.T) { //nolint:paralleltest // mutates global http.DefaultTransport
	// Mutates http.DefaultTransport, so this test must not be parallel.
	orig := http.DefaultTransport
	http.DefaultTransport = refusingTransport{t: t}
	defer func() { http.DefaultTransport = orig }()

	assert.NotPanics(t, func() {
		via.New(via.WithPlugins(maplibre.Plugin()))
	}, "Register must do zero network I/O — boot must succeed offline")
}

func TestPlugin_injectsEmbeddedJSAndCSSByDefault(t *testing.T) {
	t.Parallel()
	html := servePage(t)

	assert.Regexp(t, hashedJS, html,
		"plugin must attach the embedded MapLibre JS at a content-hashed path")
	assert.Regexp(t, hashedCSS, html,
		"the CSS is required — popups/markers/controls break without it")
	assert.Contains(t, html, `rel="stylesheet"`,
		"the CSS must be a stylesheet link")
	assert.NotContains(t, html, "cdn.jsdelivr.net",
		"no CDN reference may appear without explicit WithCDN opt-in")
}

func TestPlugin_usesPlainNotMinJSBundle(t *testing.T) {
	t.Parallel()
	// dist/maplibre-gl.js IS the minified build; a .min.js name would mean
	// a file that doesn't exist upstream got vendored.
	html := servePage(t)
	assert.NotContains(t, html, "maplibre-gl.min.js",
		"there is no .min.js bundle in dist")
}

func TestPlugin_assetsServedWithImmutableCacheHeaders(t *testing.T) {
	t.Parallel()
	html, server := servePageOn(t)

	tests := []struct {
		name        string
		pattern     *regexp.Regexp
		contentType string
		sample      string
	}{
		{"js bundle", hashedJS, "text/javascript", "MapLibre GL JS"},
		{"stylesheet", hashedCSS, "text/css", "maplibregl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := tt.pattern.FindString(html)
			require.NotEmpty(t, path, "rendered page must reference the hashed asset path")

			resp, err := server.Client().Get(server.URL + path)
			require.NoError(t, err)
			defer resp.Body.Close()

			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, tt.contentType, resp.Header.Get("Content-Type"))
			assert.Equal(t, "public, max-age=31536000, immutable",
				resp.Header.Get("Cache-Control"),
				"hashed URLs change with content, so the response may cache forever")
			body, _ := io.ReadAll(resp.Body)
			assert.Contains(t, string(body), tt.sample,
				"the served body must be the real vendored MapLibre artifact")
		})
	}
}

func TestPlugin_servesGzipWhenAccepted(t *testing.T) {
	t.Parallel()
	html, server := servePageOn(t)
	path := hashedJS.FindString(html)
	require.NotEmpty(t, path)

	req, _ := http.NewRequest("GET", server.URL+path, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", resp.Header.Get("Vary"),
		"shared caches must key per encoding")
}

func TestPlugin_returns404ForStaleHash(t *testing.T) {
	t.Parallel()
	_, server := servePageOn(t)

	resp, err := server.Client().Get(
		server.URL + "/via/assets/maplibre/0000000000000000/maplibre-gl.js")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"a hash that no longer matches the embedded content must 404, not serve a mismatched body")
}

func TestPage_rendersNoThirdPartyScriptWithoutIntegrity(t *testing.T) {
	t.Parallel()
	html := servePage(t)

	assert.NotContains(t, html, `src="http`,
		"the default plugin must not reference any third-party script origin")
	assert.NotContains(t, html, `src="//`,
		"protocol-relative third-party origins are third-party too")
	assert.NotContains(t, html, `href="http`,
		"no third-party stylesheet origins by default either")
}

func TestPlugin_WithVersion_panicsOnVersionBumpWithoutNewIntegrity(t *testing.T) {
	t.Parallel()
	// The embedded assets are compiled in at the pinned version; a bare
	// version bump has no asset (and no hash) to back it.
	assert.Panics(t, func() { maplibre.Plugin(maplibre.WithVersion("5.1.0")) },
		"bumping the pinned version without new WithCDN hashes must panic")
	assert.NotPanics(t, func() { maplibre.Plugin(maplibre.WithVersion("5.24.0")) },
		"restating the pinned version is a no-op, not an error")
}

func TestPlugin_WithSource_overridesJSURLOnly(t *testing.T) {
	t.Parallel()
	html := servePage(t, maplibre.WithSource("/static/maplibre.js"))

	assert.Contains(t, html, `src="/static/maplibre.js"`,
		"WithSource must drop in the self-hosted JS URL")
	assert.False(t, hashedJS.MatchString(html),
		"WithSource must replace the embedded JS, not append alongside it")
	assert.Regexp(t, hashedCSS, html,
		"WithSource overrides only the JS; the CSS stays embedded")
}

func TestPlugin_WithStylesheet_overridesCSSURLOnly(t *testing.T) {
	t.Parallel()
	html := servePage(t, maplibre.WithStylesheet("/static/maplibre.css"))

	assert.Contains(t, html, `href="/static/maplibre.css"`,
		"WithStylesheet must drop in the self-hosted CSS URL")
	assert.False(t, hashedCSS.MatchString(html),
		"WithStylesheet must replace the embedded CSS link, not append alongside it")
	assert.Regexp(t, hashedJS, html,
		"WithStylesheet overrides only the CSS; the JS stays embedded")
}

func TestPlugin_WithCSPBuild_usesCSPBundleAndEmbeddedWorker(t *testing.T) {
	t.Parallel()
	html := servePage(t, maplibre.WithCSPBuild())

	assert.Regexp(t, hashedCSPJS, html,
		"WithCSPBuild must load the CSP-safe bundle for strict worker-src policies")
	assert.NotContains(t, html, "/maplibre-gl.js",
		"the blob-worker default bundle must not also load under WithCSPBuild")
	assert.Regexp(t, hashedWorker, html,
		"the CSP bundle boots no worker on its own — maplibregl.workerUrl must point at the embedded worker")
	assert.Contains(t, html, "maplibregl.workerUrl=",
		"the worker URL must be assigned before any map is constructed")
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

func TestPlugin_panicsOnCrossOriginSourceWithoutIntegrity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func()
	}{
		{"WithSource https", func() { maplibre.WithSource("https://cdn.example.com/maplibre-gl.js") }},
		{"WithSource protocol-relative", func() { maplibre.WithSource("//cdn.example.com/maplibre-gl.js") }},
		{"WithStylesheet https", func() { maplibre.WithStylesheet("https://cdn.example.com/maplibre-gl.css") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Cross-origin delivery without SRI is the supply-chain hole
			// the WithCDN options' mandatory integrity closes.
			assert.Panics(t, tt.call,
				"cross-origin URLs must go through WithCDN/WithCDNStylesheet with an integrity hash")
		})
	}
}

func TestPlugin_panicsOnCDNSourceWithoutIntegrity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		integrity string
	}{
		{"empty integrity", ""},
		{"unknown algorithm", "md5-aGVsbG8="},
		{"invalid base64", "sha256-not*valid*base64!!"},
		{"digest length mismatch", "sha256-" + base64.StdEncoding.EncodeToString([]byte("short"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, func() {
				maplibre.WithCDN("https://cdn.example.com/maplibre-gl.js", tt.integrity)
			}, "a CDN source without a well-formed SRI hash must panic at registration")
			assert.Panics(t, func() {
				maplibre.WithCDNStylesheet("https://cdn.example.com/maplibre-gl.css", tt.integrity)
			}, "the CSS link is as forgeable as the script and needs the same guarantee")
		})
	}
}

func TestPlugin_emitsIntegrityAndCrossoriginForCDN(t *testing.T) {
	t.Parallel()
	jsSRI := sriFor()
	cssSRI := sriFor()
	html := servePage(t,
		maplibre.WithCDN("https://cdn.example.com/maplibre-gl.js", jsSRI),
		maplibre.WithCDNStylesheet("https://cdn.example.com/maplibre-gl.css", cssSRI),
	)

	assert.Contains(t, html, `src="https://cdn.example.com/maplibre-gl.js"`)
	assert.Contains(t, html, `href="https://cdn.example.com/maplibre-gl.css"`)
	assert.Contains(t, html, `integrity="`+jsSRI+`"`,
		"the CDN script tag must pin its content with the supplied SRI hash")
	assert.Contains(t, html, `crossorigin="anonymous"`,
		"SRI verification on a cross-origin resource requires CORS-mode fetching")
}

func TestPlugin_conflictingSourceOptionsPanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []maplibre.PluginOption
	}{
		{"WithSource + WithCDN", []maplibre.PluginOption{
			maplibre.WithSource("/static/maplibre.js"),
			maplibre.WithCDN("https://cdn.example.com/maplibre-gl.js", sriFor()),
		}},
		{"WithStylesheet + WithCDNStylesheet", []maplibre.PluginOption{
			maplibre.WithStylesheet("/static/maplibre.css"),
			maplibre.WithCDNStylesheet("https://cdn.example.com/maplibre-gl.css", sriFor()),
		}},
		{"WithCSPBuild + WithSource", []maplibre.PluginOption{
			maplibre.WithCSPBuild(),
			maplibre.WithSource("/static/maplibre.js"),
		}},
		{"WithCSPBuild + WithCDN", []maplibre.PluginOption{
			maplibre.WithCSPBuild(),
			maplibre.WithCDN("https://cdn.example.com/maplibre-gl.js", sriFor()),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, func() { maplibre.Plugin(tt.opts...) },
				"two options claiming the same resource are a programming error and must panic at registration")
		})
	}
}
