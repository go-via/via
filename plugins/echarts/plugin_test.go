package echarts_test

import (
	"io"
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

func TestPlugin_appendsCDNScriptToHead(t *testing.T) {
	t.Parallel()

	app := via.New(
		via.WithPlugins(echarts.Plugin()),
	)
	server := vt.Serve(t, app)
	via.Mount[echartsPage](app, "/")

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	assert.Contains(t, string(body), "echarts@6.0.0/dist/echarts.min.js",
		"plugin should attach the echarts CDN script tag at the documented default version")
}

func TestPlugin_WithSource_replacesCDNURLEntirely(t *testing.T) {
	t.Parallel()

	app := via.New(
		via.WithPlugins(echarts.Plugin(echarts.WithSource("/static/echarts.min.js"))),
	)
	server := vt.Serve(t, app)
	via.Mount[echartsPage](app, "/")

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	assert.Contains(t, html, `src="/static/echarts.min.js"`,
		"WithSource must drop the chart's <script> src in for self-hosted builds")
	assert.NotContains(t, html, "cdn.jsdelivr.net",
		"WithSource must replace the CDN entirely, not append alongside it")
}

func TestPlugin_WithSource_winsOverWithVersion(t *testing.T) {
	t.Parallel()

	app := via.New(
		via.WithPlugins(echarts.Plugin(
			echarts.WithVersion("5.4.3"),
			echarts.WithSource("/custom/echarts.js"),
		)),
	)
	server := vt.Serve(t, app)
	via.Mount[echartsPage](app, "/")

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	// WithSource overrides WithVersion (documented). When both are set,
	// the CDN URL must not appear at all — neither the version fragment
	// nor the cdn.jsdelivr.net host.
	assert.Contains(t, html, `src="/custom/echarts.js"`,
		"WithSource path must be used when both options are set")
	assert.NotContains(t, html, "5.4.3",
		"WithVersion must have no effect when WithSource is set")
	assert.NotContains(t, html, "cdn.jsdelivr.net",
		"the CDN host must not appear when WithSource is set")
}

func TestPlugin_versionOverridable(t *testing.T) {
	t.Parallel()

	app := via.New(
		via.WithPlugins(echarts.Plugin(echarts.WithVersion("5.4.3"))),
	)
	server := vt.Serve(t, app)
	via.Mount[echartsPage](app, "/")

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	assert.Contains(t, html, "echarts@5.4.3",
		"WithVersion should propagate into the CDN URL")
	assert.NotContains(t, html, "echarts@6.0.0",
		"the default version must not also appear — would mean WithVersion didn't fully override")
}

func TestPlugin_WithVersion_panicsOnEmptyString(t *testing.T) {
	t.Parallel()
	// Empty version produces `echarts@/dist/echarts.min.js` which 404s.
	// Reject at the option boundary so the bad call shows in the stack
	// trace rather than as "echarts won't load."
	assert.Panics(t, func() { echarts.WithVersion("") },
		"WithVersion must reject empty strings")
}

func TestPlugin_WithSource_panicsOnEmptyString(t *testing.T) {
	t.Parallel()
	// Empty source silently falls back to the CDN — that defeats the
	// purpose of opting into a custom source. Reject explicitly.
	assert.Panics(t, func() { echarts.WithSource("") },
		"WithSource must reject empty strings")
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
