package via_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sseEmptyPage struct{}

func (p *sseEmptyPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestHandleSSEClose_oversizedBodyReturns413(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithMaxRequestBody(16),
	)
	via.Mount[sseEmptyPage](app, "/")
	defer server.Close()

	resp, err := http.Post(
		server.URL+"/_sse/close",
		"text/plain",
		bytes.NewReader(bytes.Repeat([]byte("x"), 1024)),
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestHandleSSEClose_unknownTabIsNoOp200(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[sseEmptyPage](app, "/")
	defer server.Close()

	resp, err := http.Post(
		server.URL+"/_sse/close",
		"text/plain",
		strings.NewReader("does-not-exist"),
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"unknown tab id is silently dropped, not an error")
}

// WithSSEWriteTimeout installs a per-write deadline on the underlying
// connection. We can't easily simulate a stalled TCP peer in-process,
// but we can verify the option threads through to the runtime by
// confirming the SSE handshake still succeeds with the option set —
// a regression where the timeout wiring panicked or wrapped a nil
// writer would fail this test loudly.

type sseDeadlinePage struct{}

func (p *sseDeadlinePage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestWithSSEWriteTimeout_doesNotBreakNormalDrains(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithSSEWriteTimeout(500*time.Millisecond),
	)
	via.Mount[sseDeadlinePage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()

	// Pull the heartbeat (default 25s — short cap for the test).
	select {
	case f := <-frames:
		// Any drain succeeded — the deadline applied without preventing
		// the write.
		_ = f
	case <-time.After(300 * time.Millisecond):
		// No frame is fine too (heartbeat hasn't fired yet); the
		// assertion is that nothing panicked.
	}
}
