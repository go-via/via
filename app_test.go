package via_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startServer(t *testing.T, app *via.App) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(app.HTTPServeMux())
	t.Cleanup(server.Close)
	return server
}

func TestNew_returnsNonNil(t *testing.T) {
	v := via.New()
	assert.NotNil(t, v)
}

func TestNew_appliesTitle(t *testing.T) {
	app := via.New(via.WithTitle("My App"))
	app.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("hello")) })
	})
	server := startServer(t, app)
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "My App")
}

func TestNew_usesDefaultTitle(t *testing.T) {
	app := via.New()
	app.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("hello")) })
	})
	server := startServer(t, app)
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "Via")
}

func TestPage_rendersViewInDocument(t *testing.T) {
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.H1(h.Text("Hello Via!")) })
	})
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "Hello Via!")
}

func TestPage_includesDatastarScript(t *testing.T) {
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "_datastar.js")
}

func TestPage_includesViaCtxSignal(t *testing.T) {
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "via_tab")
}

func TestPage_panicsOnNilView(t *testing.T) {
	v := via.New()
	assert.Panics(t, func() {
		v.Page("/", func(cmp *via.Cmp) {
			cmp.View(nil)
		})
	})
}

func TestPage_rendersPathParam(t *testing.T) {
	server := newTestApp(t, "/users/{id}", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			id := ctx.GetPathParam("id")
			return h.Div(h.Textf("user-%s", id))
		})
	})
	body := getPageBody(t, server, "/users/42")
	assert.Contains(t, body, "user-42")
}

func TestAppendToHead_addsElement(t *testing.T) {
	v := via.New()
	v.AppendToHead(h.Link(h.Rel("stylesheet"), h.Href("/app.css")))
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	server := startServer(t, v)
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "/app.css")
}

func TestAppendToFoot_addsElement(t *testing.T) {
	v := via.New()
	v.AppendToFoot(h.Script(h.Src("/foot.js")))
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	server := startServer(t, v)
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "/foot.js")
}

func TestPage_embedsInitialSignalValuesInHTML(t *testing.T) {
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		s := via.Signal(cmp, 42)
		s.Tag("count")
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(s.Text()) })
	})
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, `count_`, "signal display ID must appear in initial HTML")
	assert.Contains(t, body, `42`, "signal initial value must appear in initial HTML")
}

func TestNew_acceptsShutdownTimeout(t *testing.T) {
	v := via.New(via.WithShutdownTimeout(10 * time.Second))
	assert.NotNil(t, v)
}

func TestShutdown_disposesActiveContexts(t *testing.T) {
	t.Parallel()
	disposed := make(chan struct{})
	app := via.New()
	app.Page("/", func(cmp *via.Cmp) {
		cmp.Dispose(func() { close(disposed) })
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	server := startServer(t, app)

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	err := app.Shutdown(context.Background())
	assert.NoError(t, err)

	select {
	case <-disposed:
	case <-time.After(2 * time.Second):
		t.Fatal("dispose callback not called after Shutdown")
	}
}

func TestShutdown_disposesComponentsOfActiveContexts(t *testing.T) {
	t.Parallel()
	disposed := make(chan struct{})
	app := via.New()
	app.Page("/", func(cmp *via.Cmp) {
		cmp.Component(func(comp *via.Cmp) {
			comp.Dispose(func() { close(disposed) })
			comp.View(func(ctx *via.Ctx) h.H { return h.Span() })
		})
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	server := startServer(t, app)

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	require.NoError(t, app.Shutdown(context.Background()))

	select {
	case <-disposed:
	case <-time.After(2 * time.Second):
		t.Fatal("component dispose callback not called after Shutdown")
	}
}

func TestShutdown_closesSSEStream(t *testing.T) {
	t.Parallel()
	app := via.New()
	app.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	server := startServer(t, app)

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	scanner, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(50 * time.Millisecond)

	require.NoError(t, app.Shutdown(context.Background()))

	done := make(chan struct{})
	go func() {
		for scanner.Scan() {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE stream did not close after Shutdown")
	}
}

func TestDispose_shutdownAfterSSECloseDoesNotPanic(t *testing.T) {
	t.Parallel()

	disposeCount := 0
	app := via.New()
	app.Page("/", func(cmp *via.Cmp) {
		cmp.Dispose(func() { disposeCount++ })
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	server := startServer(t, app)

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	// Close via beacon first
	req, _ := http.NewRequest("POST", server.URL+"/_sse/close", bytes.NewBufferString(ctxID))
	r, _ := http.DefaultClient.Do(req)
	if r != nil {
		r.Body.Close()
	}

	time.Sleep(50 * time.Millisecond)

	// SSE disconnect triggers disposeCtx on same ctx — must not panic
	assert.NotPanics(t, func() {
		cancel()
		time.Sleep(50 * time.Millisecond)
	})

	assert.Equal(t, 1, disposeCount, "dispose callback must run exactly once")
}

func TestDispose_panickingCallbackDoesNotCrashServer(t *testing.T) {
	t.Parallel()

	app := via.New()
	app.Page("/", func(cmp *via.Cmp) {
		cmp.Dispose(func() { panic("boom") })
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	server := startServer(t, app)

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	// Close session — dispose panics but server must survive
	req, _ := http.NewRequest("POST", server.URL+"/_sse/close", bytes.NewBufferString(ctxID))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	// Server must still be alive
	resp2, err := http.Get(server.URL + "/_datastar.js")
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode, "server must survive panicking dispose callback")
}

func TestShutdown_succeedsWithNoActiveContexts(t *testing.T) {
	t.Parallel()
	app := via.New()
	app.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	err := app.Shutdown(context.Background())
	assert.NoError(t, err)
}

func TestDatastarJS_served(t *testing.T) {
	v := via.New()
	server := startServer(t, v)
	resp, err := http.Get(server.URL + "/_datastar.js")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
