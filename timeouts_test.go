package via_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	// Start binds and runs ListenAndServe in this goroutine, so spin
	// it up in a goroutine and shut it down once we've snapshotted.
	go app.Start()

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		s := captured
		mu.Unlock()
		if s != nil {
			assert.Equal(t, 7*time.Second, s.ReadHeaderTimeout)
			assert.Equal(t, 15*time.Second, s.ReadTimeout)
			assert.Equal(t, 20*time.Second, s.WriteTimeout)
			assert.Equal(t, 45*time.Second, s.IdleTimeout)
			require.NoError(t, app.Shutdown(context.Background()))
			return
		}
		select {
		case <-deadline:
			t.Fatal("WithHTTPServer hook never ran; Start did not bind")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
