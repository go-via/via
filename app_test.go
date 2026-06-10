package via_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApp_servesDatastarJS(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)

	resp, err := server.Client().Get(server.URL + "/_datastar.js")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestApp_routes404ForUnknownPath(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	app.HandleFunc("/known", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("known"))
	})

	resp, err := server.Client().Get(server.URL + "/unknown-path")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestApp_handlesMultipleRoutes(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	app.HandleFunc("/first", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("first"))
	})
	app.HandleFunc("/second", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("second"))
	})

	resp1, err := server.Client().Get(server.URL + "/first")
	require.NoError(t, err)
	buf1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	assert.Contains(t, string(buf1), "first")

	resp2, err := server.Client().Get(server.URL + "/second")
	require.NoError(t, err)
	buf2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	assert.Contains(t, string(buf2), "second")
}

func TestApp_builtinEndpointsReject404OnUnknownTab(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)

	cases := []struct {
		name string
		do   func() (*http.Response, error)
	}{
		{"GET /_sse", func() (*http.Response, error) {
			return server.Client().Get(server.URL + "/_sse")
		}},
		{"POST /_action/Inc", func() (*http.Response, error) {
			return server.Client().Post(server.URL+"/_action/Inc", "text/plain", nil)
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

	app := via.New()
	server := vt.Serve(t, app)
	app.Handle("/raw", customHandler{})

	resp, err := server.Client().Get(server.URL + "/raw")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "custom-handle")
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

func (pluginHostPage) View(ctx *via.CtxR) h.H { return h.Div(h.Text("page")) }

func TestUse_concurrentBootCallsKeepAllMiddlewareInChain(t *testing.T) {
	t.Parallel()

	const N = 32
	app := via.New()
	server := vt.Serve(t, app)

	var counter int32
	var counterMu sync.Mutex
	mw := func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		counterMu.Lock()
		counter++
		counterMu.Unlock()
		next.ServeHTTP(w, r)
	}

	// Two concurrent Use calls race on a.middleware append without a
	// guard — the race detector flags the slice write, and lost entries
	// would surface as a counter lower than N after one request.
	var wg sync.WaitGroup
	wg.Add(N)
	for range N {
		go func() {
			defer wg.Done()
			app.Use(mw)
		}()
	}
	wg.Wait()

	app.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})

	resp, err := server.Client().Get(server.URL + "/ping")
	require.NoError(t, err)
	resp.Body.Close()

	counterMu.Lock()
	got := counter
	counterMu.Unlock()
	assert.Equal(t, int32(N), got,
		"every concurrently-registered middleware must be in the chain")
}

func TestApp_pluginRegistrationInjectsDocumentAndAppSignals(t *testing.T) {
	t.Parallel()

	app := via.New(
		via.WithPlugins(signalSeedingPlugin{key: "_pluginKey", val: "seeded"}),
	)
	server := vt.Serve(t, app)
	via.Mount[pluginHostPage](app, "/")

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

type useAfterStartPage struct{}

func (p *useAfterStartPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestAppUse_afterStartPanics(t *testing.T) {
	t.Parallel()

	app := via.New(via.WithAddr("127.0.0.1:0"))
	via.Mount[useAfterStartPage](app, "/")

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

// Two plugins (or a plugin and user code) registering the same app-signal key
// would silently clobber each other's initial value — exactly the
// registration-time programming mistake CONVENTIONS says must panic, and the
// twin of the route-collision check. picocss owns "_picoTheme"/"_picoDarkMode".
func TestRegisterAppSignal_panicsOnDuplicateKey(t *testing.T) {
	t.Parallel()
	app := via.New()
	app.RegisterAppSignal("dup", 1)
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "registering a duplicate app-signal key must panic")
		msg, _ := rec.(string)
		assert.Contains(t, msg, "duplicate")
		assert.Contains(t, msg, "dup", "panic must name the colliding key")
	}()
	app.RegisterAppSignal("dup", 2)
}

// Run must return a bind failure (the textbook runtime/external error) instead
// of panicking, so callers can handle "address already in use" with log.Fatal
// rather than a stack trace. Start stays the panic-on-error convenience wrapper.
func TestRun_returnsBindErrorInsteadOfPanicking(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	app := via.New(via.WithAddr(ln.Addr().String())) // address already in use
	via.Mount[simpleCounter](app, "/")
	require.Error(t, app.Run(), "Run must return the bind error, not panic")
}

// Clearly-invalid (negative) option values are a registration-time programming
// mistake and must panic at New, not silently produce a broken server (a
// negative shutdown timeout → instant ungraceful kill; negative size caps →
// nonsense). 0 stays valid (it means unlimited/default for these knobs).
func TestNew_panicsOnNegativeOptionValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		opt  via.Option
		want string
	}{
		{"shutdown timeout", via.WithShutdownTimeout(-time.Second), "WithShutdownTimeout"},
		{"max request body", via.WithMaxRequestBody(-1), "WithMaxRequestBody"},
		{"max upload size", via.WithMaxUploadSize(-1), "WithMaxUploadSize"},
		{"max contexts", via.WithMaxContexts(-1), "WithMaxContexts"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				rec := recover()
				require.NotNil(t, rec, "a negative %s must panic at New", c.name)
				msg, _ := rec.(string)
				assert.Contains(t, msg, c.want, "panic must name the offending option")
			}()
			via.New(c.opt)
		})
	}
}
