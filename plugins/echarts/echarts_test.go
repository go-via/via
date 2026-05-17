package echarts_test

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/echarts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type echartsPage struct{}

func (p *echartsPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestPlugin_appendsCDNScriptToHead(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithPlugins(echarts.Plugin()),
		via.WithTestServer(&server),
	)
	via.Mount[echartsPage](app, "/")
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	assert.Contains(t, string(body), "echarts@",
		"plugin should attach the echarts CDN script tag")
	assert.Contains(t, string(body), "echarts.min.js")
}

func TestPlugin_versionOverridable(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithPlugins(echarts.Plugin(echarts.WithVersion("5.4.3"))),
		via.WithTestServer(&server),
	)
	via.Mount[echartsPage](app, "/")
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "echarts@5.4.3",
		"WithVersion should propagate into the CDN URL")
}

func TestNewChart_assignsAutoIDWhenUnset(t *testing.T) {
	t.Parallel()

	c := echarts.NewChart()
	assert.NotPanics(t, func() {
		_ = c.Mount() // should render without an explicit ID
	}, "NewChart with no options should still produce a usable Mount node")
}
