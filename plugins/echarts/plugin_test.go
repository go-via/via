package echarts_test

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/echarts"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type echartsPage struct{}

func (p *echartsPage) View(ctx *via.CtxR) h.H { return h.Div() }

// renderHome boots a one-page app with the given plugin options and
// returns the rendered document HTML plus the live server.
func renderHome(t *testing.T, opts ...echarts.PluginOption) (string, *httptest.Server) {
	t.Helper()
	app := via.New(via.WithPlugins(echarts.Plugin(opts...)))
	server := vt.Serve(t, app)
	via.Mount[echartsPage](app, "/")
	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body), server
}

// validIntegrity builds a syntactically valid sha384 SRI value; the
// digest bytes are arbitrary because only the grammar is validated at
// registration.
func validIntegrity() string {
	return "sha384-" + base64.StdEncoding.EncodeToString(make([]byte, 48))
}

var hashedJSPath = regexp.MustCompile(`/via/assets/echarts/[0-9a-f]+/echarts\.min\.js`)

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
		via.New(via.WithPlugins(echarts.Plugin()))
	}, "Register must do zero network I/O — boot must succeed offline")
}

func TestPlugin_servesEmbeddedScriptByDefault(t *testing.T) {
	t.Parallel()
	html, _ := renderHome(t)

	assert.Regexp(t, hashedJSPath, html,
		"the default script tag must point at the content-hashed embedded asset")
	assert.NotContains(t, html, "cdn.jsdelivr.net",
		"no CDN reference may appear without explicit WithCDN opt-in")
}

func TestPlugin_assetServedWithImmutableCacheHeader(t *testing.T) {
	t.Parallel()
	html, server := renderHome(t)

	path := hashedJSPath.FindString(html)
	require.NotEmpty(t, path, "rendered page must reference the hashed asset path")

	resp, err := server.Client().Get(server.URL + path)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/javascript", resp.Header.Get("Content-Type"))
	assert.Equal(t, "public, max-age=31536000, immutable",
		resp.Header.Get("Cache-Control"),
		"hashed URLs change with content, so the response may cache forever")
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "echarts",
		"the served body must be the real vendored echarts build")
}

func TestPlugin_servesGzipWhenAccepted(t *testing.T) {
	t.Parallel()
	html, server := renderHome(t)
	path := hashedJSPath.FindString(html)
	require.NotEmpty(t, path)

	req, _ := http.NewRequest("GET", server.URL+path, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", resp.Header.Get("Vary"),
		"shared caches must key per encoding")

	idReq, _ := http.NewRequest("GET", server.URL+path, nil)
	idReq.Header.Set("Accept-Encoding", "identity")
	idResp, err := server.Client().Do(idReq)
	require.NoError(t, err)
	defer idResp.Body.Close()
	assert.Empty(t, idResp.Header.Get("Content-Encoding"),
		"without gzip in Accept-Encoding the asset must be served uncompressed")
}

func TestPlugin_returns404ForStaleHash(t *testing.T) {
	t.Parallel()
	app := via.New(via.WithPlugins(echarts.Plugin()))
	server := vt.Serve(t, app)

	resp, err := server.Client().Get(
		server.URL + "/via/assets/echarts/0000000000000000/echarts.min.js")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"a hash that no longer matches the embedded content must 404, not serve a mismatched body")
}

func TestPage_rendersNoThirdPartyScriptWithoutIntegrity(t *testing.T) {
	t.Parallel()
	html, _ := renderHome(t)

	assert.NotContains(t, html, `src="http`,
		"the default plugin must not reference any third-party script origin")
	assert.NotContains(t, html, `src="//`,
		"protocol-relative third-party origins are third-party too")
	assert.NotContains(t, html, `href="http`,
		"no third-party stylesheet/link origins by default either")
}

func TestPlugin_WithSource_replacesEmbeddedScript(t *testing.T) {
	t.Parallel()
	html, _ := renderHome(t, echarts.WithSource("/static/echarts.min.js"))

	assert.Contains(t, html, `src="/static/echarts.min.js"`,
		"WithSource must drop the chart's <script> src in for self-hosted builds")
	assert.False(t, hashedJSPath.MatchString(html),
		"WithSource must replace the embedded script tag, not append alongside it")
}

func TestPlugin_WithSource_panicsOnEmptyString(t *testing.T) {
	t.Parallel()
	// Empty source silently falls back to the embedded asset — that
	// defeats the purpose of opting into a custom source. Reject explicitly.
	assert.Panics(t, func() { echarts.WithSource("") },
		"WithSource must reject empty strings")
}

func TestPlugin_WithSource_panicsOnCrossOriginURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
	}{
		{"https origin", "https://cdn.example.com/echarts.min.js"},
		{"http origin", "http://cdn.example.com/echarts.min.js"},
		{"protocol-relative origin", "//cdn.example.com/echarts.min.js"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Cross-origin script delivery without SRI is exactly the
			// supply-chain hole WithCDN's mandatory integrity closes.
			assert.Panics(t, func() { echarts.WithSource(tt.url) },
				"cross-origin sources must go through WithCDN(url, integrity)")
		})
	}
}

func TestPlugin_WithVersion_panicsOnEmptyString(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { echarts.WithVersion("") },
		"WithVersion must reject empty strings")
}

func TestPlugin_WithVersion_panicsOnVersionBumpWithoutNewIntegrity(t *testing.T) {
	t.Parallel()
	// The embedded asset is compiled in at the pinned version; a bare
	// version bump has no asset (and no hash) to back it.
	assert.Panics(t, func() { echarts.Plugin(echarts.WithVersion("5.4.3")) },
		"bumping the pinned version without a new WithCDN hash must panic")
	assert.NotPanics(t, func() { echarts.Plugin(echarts.WithVersion("6.0.0")) },
		"restating the pinned version is a no-op, not an error")
}

func TestPlugin_panicsOnCDNSourceWithoutIntegrity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		integrity string
	}{
		{"empty integrity", ""},
		{"unknown algorithm", "md5-aGVsbG8="},
		{"missing algorithm prefix", base64.StdEncoding.EncodeToString(make([]byte, 48))},
		{"invalid base64", "sha384-not*valid*base64!!"},
		{"digest length mismatch", "sha384-" + base64.StdEncoding.EncodeToString([]byte("short"))},
		{"empty digest", "sha512-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, func() {
				echarts.WithCDN("https://cdn.example.com/echarts@6.0.0/dist/echarts.min.js", tt.integrity)
			}, "a CDN source without a well-formed SRI hash must panic at registration")
		})
	}
}

func TestPlugin_WithCDN_panicsOnNonHTTPSURL(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		echarts.WithCDN("/local/echarts.min.js", validIntegrity())
	}, "WithCDN is for cross-origin delivery; same-origin paths belong to WithSource")
}

func TestPlugin_emitsIntegrityAndCrossoriginForCDN(t *testing.T) {
	t.Parallel()
	integrity := validIntegrity()
	html, _ := renderHome(t,
		echarts.WithCDN("https://cdn.example.com/echarts@6.0.0/dist/echarts.min.js", integrity))

	assert.Contains(t, html, `src="https://cdn.example.com/echarts@6.0.0/dist/echarts.min.js"`)
	assert.Contains(t, html, `integrity="`+integrity+`"`,
		"the CDN script tag must pin its content with the supplied SRI hash")
	assert.Contains(t, html, `crossorigin="anonymous"`,
		"SRI verification on a cross-origin script requires CORS-mode fetching")
}

func TestPlugin_WithCDN_conflictsWithWithSource(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		echarts.Plugin(
			echarts.WithSource("/static/echarts.min.js"),
			echarts.WithCDN("https://cdn.example.com/e.js", validIntegrity()),
		)
	}, "two script-source options are a programming error and must panic at registration")
	assert.Panics(t, func() {
		echarts.Plugin(
			echarts.WithCDN("https://cdn.example.com/e.js", validIntegrity()),
			echarts.WithSource("/static/echarts.min.js"),
		)
	}, "the conflict must panic regardless of option order")
}

func TestNewChart_assignsAutoIDWhenUnset(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart()
	var sb strings.Builder
	require.NoError(t, c.Mount().Render(&sb))

	// Auto-ids follow the `echart-<seq>` shape; the seq increments
	// across charts so we don't pin the number, just the format.
	assert.Contains(t, sb.String(), `id="echart-`,
		"NewChart without WithElementID must auto-generate an id matching `echart-<seq>`")
}

func TestNewChart_autoIDsAreUniqueAcrossCharts(t *testing.T) {
	t.Parallel()

	// Two charts mounted on the same page must end up at distinct
	// registry slots (window.__viaCharts[N]) and distinct DOM ids —
	// otherwise the second chart's init script would clobber the
	// first's registry entry and both observers would point at the
	// same DOM element.
	a := echarts.NewChart()
	b := echarts.NewChart()

	var sa, sb strings.Builder
	require.NoError(t, a.Mount().Render(&sa))
	require.NoError(t, b.Mount().Render(&sb))

	assert.NotEqual(t, sa.String(), sb.String(),
		"two auto-id charts must render with different seq numbers")
}
