package via_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WithLang / WithDescription — document metadata

type langPage struct{}

func (p *langPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestRender_documentMetadataOptions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		opts     []via.Option
		contains []string
		excludes []string
	}{
		{
			name:     "WithLang sets html lang attribute",
			opts:     []via.Option{via.WithLang("en")},
			contains: []string{`<html lang="en">`},
		},
		{
			name:     "WithDescription sets description meta",
			opts:     []via.Option{via.WithDescription("A reactive Go demo.")},
			contains: []string{`<meta name="description"`, "A reactive Go demo."},
		},
		{
			name:     "Lang unset emits no attribute",
			excludes: []string{`<html lang="`},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			app := via.New(c.opts...)
			server := vt.Serve(t, app)
			via.Mount[langPage](app, "/")
			body := getBody(t, server, "/")
			for _, n := range c.contains {
				assert.Contains(t, body, n)
			}
			for _, n := range c.excludes {
				assert.NotContains(t, body, n)
			}
		})
	}
}

// WithMaxContexts / LiveTabs

type maxCtxPage struct{}

func (p *maxCtxPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestMaxContexts_rejectsBeyondCap(t *testing.T) {
	t.Parallel()

	app := via.New(
		via.WithMaxContexts(2),
	)
	server := vt.Serve(t, app)
	via.Mount[maxCtxPage](app, "/")

	for range 2 {
		resp, err := server.Client().Get(server.URL + "/")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"first %d requests should fit under the cap", 2)
	}

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode,
		"third request should be 503 with cap=2")
}

func TestMaxContexts_zeroDisablesTheCap(t *testing.T) {
	t.Parallel()

	app := via.New() // no WithMaxContexts
	server := vt.Serve(t, app)
	via.Mount[maxCtxPage](app, "/")

	for range 5 {
		resp, err := server.Client().Get(server.URL + "/")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"unset cap should not reject any request")
	}
}

// WithRead/Write/Idle/HeaderTimeout / WithHTTPServer

func TestHTTPServer_constructsConfiguredServer(t *testing.T) {
	t.Parallel()

	app := via.New(
		via.WithAddr(":4242"),
		via.WithReadHeaderTimeout(8*time.Second),
		via.WithReadTimeout(11*time.Second),
		via.WithWriteTimeout(0),
		via.WithIdleTimeout(99*time.Second),
	)

	srv := app.HTTPServer()
	require.NotNil(t, srv)
	assert.Equal(t, ":4242", srv.Addr)
	assert.Equal(t, 8*time.Second, srv.ReadHeaderTimeout)
	assert.Equal(t, 11*time.Second, srv.ReadTimeout)
	assert.Equal(t, time.Duration(0), srv.WriteTimeout)
	assert.Equal(t, 99*time.Second, srv.IdleTimeout)
	assert.NotNil(t, srv.Handler, "server.Handler should be set")
}

func TestHTTPServer_appliesWithHTTPServerHook(t *testing.T) {
	t.Parallel()

	app := via.New(via.WithHTTPServer(func(s *http.Server) {
		s.MaxHeaderBytes = 4096
	}))
	srv := app.HTTPServer()
	assert.Equal(t, 4096, srv.MaxHeaderBytes)
}

func TestWithTimeouts_passThroughToHTTPServer(t *testing.T) {
	t.Parallel()

	var (
		captured *http.Server
		mu       sync.Mutex
	)
	app := via.New(
		via.WithAddr("127.0.0.1:0"),
		via.WithReadHeaderTimeout(7*time.Second),
		via.WithReadTimeout(15*time.Second),
		via.WithWriteTimeout(20*time.Second),
		via.WithIdleTimeout(45*time.Second),
		via.WithHTTPServer(func(s *http.Server) {
			mu.Lock()
			captured = s
			mu.Unlock()
		}),
	)

	go app.Start()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return captured != nil
	}, 2*time.Second, 20*time.Millisecond,
		"WithHTTPServer hook never ran; Start did not bind")

	mu.Lock()
	s := captured
	mu.Unlock()
	assert.Equal(t, 7*time.Second, s.ReadHeaderTimeout)
	assert.Equal(t, 15*time.Second, s.ReadTimeout)
	assert.Equal(t, 20*time.Second, s.WriteTimeout)
	assert.Equal(t, 45*time.Second, s.IdleTimeout)
	require.NoError(t, app.Shutdown(context.Background()))
}

type noopPlugin struct{ called *bool }

func (p noopPlugin) Register(*via.App) { *p.called = true }

func TestConfig_optionsApplyWithoutPanic(t *testing.T) {
	t.Parallel()

	pluginRan := false
	app := via.New(
		via.WithShutdownTimeout(2*time.Second),
		via.WithSessionTTL(time.Hour),
		via.WithContextTTL(10*time.Minute),
		via.WithSSEHeartbeat(30*time.Second),
		via.WithPlugins(noopPlugin{called: &pluginRan}),
	)
	require.NotNil(t, app,
		"New must return a non-nil App after applying every timeout option")
	assert.True(t, pluginRan,
		"WithPlugins must dispatch Register at New time, before serving")
}
