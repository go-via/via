package via_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type emptyPage struct{}

func (p *emptyPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestHandleSSEClose_oversizedBodyReturns413(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithMaxRequestBody(16),
	)
	via.Mount[emptyPage](app, "/")
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
	via.Mount[emptyPage](app, "/")
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

func TestRecover_panicAfterPartialWriteKeepsServerAlive(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.Recover(app))
	app.HandleFunc("/half", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("after-write")
	})
	app.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("alive"))
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/half")
	require.NoError(t, err)
	body := readAll(t, resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"headers already flushed → Recover cannot rewrite to 500")
	assert.Contains(t, body, "partial")

	resp2, err := http.Get(server.URL + "/ok")
	require.NoError(t, err)
	body2 := readAll(t, resp2.Body)
	resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode,
		"server survives panic after partial write")
	assert.Equal(t, "alive", body2)
}

type marshalUnfriendly struct {
	C chan int
}

type unmarshalablePage struct {
	Bad via.Signal[marshalUnfriendly]
}

func (p *unmarshalablePage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestWritePageDocument_marshalFailureStillRenders(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[unmarshalablePage](app, "/")
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	body := readAll(t, resp.Body)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// A typed Signal whose value can't be JSON-encoded must not poison
	// the whole page render; the document still ships, the bad signal
	// is just omitted from the initial data-signals payload.
	assert.Contains(t, body, "<div>")
}

type streamPanicPage struct {
	ticks via.Signal[int]
}

func (p *streamPanicPage) OnConnect(ctx *via.Ctx) error {
	via.Stream(ctx, 5*time.Millisecond, func(c *via.Ctx, _ time.Time) {
		panic("stream-callback-boom")
	})
	return nil
}

func (p *streamPanicPage) View(ctx *via.Ctx) h.H { return h.Div(p.ticks.Text()) }

func TestStream_callbackPanicDoesNotCrashServer(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithLogLevel(via.LogError),
	)
	via.Mount[streamPanicPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	_, cancel := tc.SSE()
	defer cancel()

	time.Sleep(30 * time.Millisecond)

	// If recoverLog didn't catch the panic, the server goroutine would
	// be dead and the follow-up GET would fail. Surviving the request
	// is the assertion.
	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAppUse_afterStartPanics(t *testing.T) {
	t.Parallel()

	app := via.New(via.WithAddr("127.0.0.1:0"))
	via.Mount[emptyPage](app, "/")

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.Start()
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = app.Shutdown(ctx)
		<-done
	})

	// Spin briefly until Start has flipped a.server. Start sets it
	// synchronously before ListenAndServe, so 200ms is generous.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if app.LiveTabs() >= 0 { // touches App, just to settle goroutine
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)

	defer func() {
		rec := recover()
		require.NotNil(t, rec, "App.Use after Start must panic")
		msg, _ := rec.(string)
		assert.Contains(t, msg, "App.Use called after Start",
			"panic must state the violation so the user spots the boot-only contract")
		assert.Contains(t, msg, "boot",
			"panic must hint at the fix (install middleware during boot)")
	}()
	app.Use(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		next.ServeHTTP(w, r)
	})
}
