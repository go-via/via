package via_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApp_servesDatastarJS(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	via.New(via.WithTestServer(&server))
	defer server.Close()

	resp, err := http.Get(server.URL + "/_datastar.js")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestApp_routes404ForUnknownPath(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.HandleFunc("/known", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("known"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/unknown-path")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestApp_handlesMultipleRoutes(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.HandleFunc("/first", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("first"))
	})
	app.HandleFunc("/second", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("second"))
	})
	defer server.Close()

	resp1, err := http.Get(server.URL + "/first")
	require.NoError(t, err)
	buf1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	assert.Contains(t, string(buf1), "first")

	resp2, err := http.Get(server.URL + "/second")
	require.NoError(t, err)
	buf2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	assert.Contains(t, string(buf2), "second")
}

func TestApp_builtinEndpointsReject404OnUnknownTab(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	via.New(via.WithTestServer(&server))
	t.Cleanup(func() { server.Close() })

	cases := []struct {
		name string
		do   func() (*http.Response, error)
	}{
		{"GET /_sse", func() (*http.Response, error) {
			return http.Get(server.URL + "/_sse")
		}},
		{"POST /_action/Inc", func() (*http.Response, error) {
			return http.Post(server.URL+"/_action/Inc", "text/plain", nil)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			resp, err := c.do()
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		})
	}
}

func TestApp_implementsHTTPHandler(t *testing.T) {
	t.Parallel()
	var _ http.Handler = via.New()
}

type customHandler struct{}

func (customHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte("custom-handle"))
}

func TestApp_Handle_routesCustomPath(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Handle("/raw", customHandler{})
	defer server.Close()

	resp, err := http.Get(server.URL + "/raw")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "custom-handle", string(body))
}

func TestApp_ServeHTTP_dispatchesThroughHandler(t *testing.T) {
	t.Parallel()
	app := via.New()
	app.HandleFunc("/direct", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("direct"))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/direct", nil)
	app.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "direct", rec.Body.String())
}

type signalSeedingPlugin struct {
	key string
	val any
}

func (p signalSeedingPlugin) Register(app *via.App) {
	app.RegisterAppSignal(p.key, p.val)
	app.AppendToHead(h.Meta(h.Name("plugin-head"), h.Content("yes")))
	app.AppendToFoot(h.Script(h.Type("text/plain"), h.Text("plugin-foot")))
	app.AppendAttrToHTML(h.Attr("data-plugin", "active"))
}

type pluginHostPage struct{}

func (pluginHostPage) View(ctx *via.Ctx) h.H { return h.Div(h.Text("page")) }

func TestApp_pluginRegistrationInjectsDocumentAndAppSignals(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithPlugins(signalSeedingPlugin{key: "_pluginKey", val: "seeded"}),
	)
	via.Mount[pluginHostPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-plugin="active"`,
		"AppendAttrToHTML must surface on <html>")
	assert.Contains(t, body, `name="plugin-head"`,
		"AppendToHead must inject into <head>")
	assert.Contains(t, body, "plugin-foot",
		"AppendToFoot must inject before </body>")
	assert.Contains(t, body, "_pluginKey",
		"RegisterAppSignal must seed the data-signals payload")
	assert.Contains(t, body, "seeded")
}
