package via_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type introspectPage struct{}

func (p *introspectPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestRoutes_returnsRegisteredPatternsWithRegistrarTag(t *testing.T) {
	t.Parallel()

	app := via.New()
	via.Mount[introspectPage](app, "/dashboard")
	app.HandleFunc("/api/health", func(http.ResponseWriter, *http.Request) {})

	routes := app.Routes()

	patterns := map[string]string{}
	for _, r := range routes {
		patterns[r.Pattern] = r.RegisteredBy
	}

	assert.Contains(t, patterns, "GET /dashboard")
	assert.Contains(t, patterns["GET /dashboard"], "Mount[introspectPage]")
	assert.Contains(t, patterns, "/api/health")
	assert.Equal(t, "HandleFunc", patterns["/api/health"])
}

func TestRoutes_orderedAlphabetically(t *testing.T) {
	t.Parallel()

	app := via.New()
	app.HandleFunc("/zeta", func(http.ResponseWriter, *http.Request) {})
	app.HandleFunc("/alpha", func(http.ResponseWriter, *http.Request) {})
	app.HandleFunc("/middle", func(http.ResponseWriter, *http.Request) {})

	routes := app.Routes()
	last := ""
	for _, r := range routes {
		assert.True(t, last <= r.Pattern,
			"routes should be sorted; %s came after %s", r.Pattern, last)
		last = r.Pattern
	}
}

type infoA struct{}

func (a *infoA) View(ctx *via.Ctx) h.H { return h.Div() }

type infoB struct{}

func (b *infoB) View(ctx *via.Ctx) h.H { return h.Div() }

func TestCompositions_listsMountedTypesSorted(t *testing.T) {
	t.Parallel()

	app := via.New()
	via.Mount[infoB](app, "/zeta")
	via.Mount[infoA](app, "/alpha")

	cs := app.Compositions()
	require.Equal(t, 2, len(cs))
	assert.Equal(t, "/alpha", cs[0].Route, "should be sorted by route")
	assert.Contains(t, cs[0].Type, "infoA")
	assert.Equal(t, "/zeta", cs[1].Route)
	assert.Contains(t, cs[1].Type, "infoB")
}

func TestWithNotFound_servesCustomHandlerOnUnknownRoute(t *testing.T) {
	t.Parallel()

	custom := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("missing"))
	})
	var server *httptest.Server
	app := via.New(
		via.WithNotFound(custom),
		via.WithTestServer(&server),
	)
	via.Mount[introspectPage](app, "/known")
	defer server.Close()

	resp, err := http.Get(server.URL + "/no-such-thing")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusTeapot, resp.StatusCode,
		"WithNotFound handler must be invoked for unmatched routes")
}

func TestWithNotFound_doesNotInterceptKnownRoutes(t *testing.T) {
	t.Parallel()

	custom := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("not-found handler must not intercept matched routes")
	})
	var server *httptest.Server
	app := via.New(
		via.WithNotFound(custom),
		via.WithTestServer(&server),
	)
	via.Mount[introspectPage](app, "/known")
	defer server.Close()

	resp, err := http.Get(server.URL + "/known")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// Conflict detection — duplicate-mount / collision-with-Handle / etc.

type pageA struct{}

func (a *pageA) View(ctx *via.Ctx) h.H { return h.Div() }

type pageB struct{}

func (b *pageB) View(ctx *via.Ctx) h.H { return h.Div() }

func TestRoute_panicsOnDuplicateMount(t *testing.T) {
	t.Parallel()

	app := via.New()
	via.Mount[pageA](app, "/dup")

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate Mount route")
		}
		msg, _ := r.(string)
		assert.True(t, strings.Contains(msg, "/dup") &&
			strings.Contains(msg, "already registered"),
			"panic should name the route and reason; got %q", msg)
	}()
	via.Mount[pageB](app, "/dup")
}

func TestRoute_panicsOnHandleFuncCollidingWithMount(t *testing.T) {
	t.Parallel()

	app := via.New()
	via.Mount[pageA](app, "/x")

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on HandleFunc colliding with Mount")
		}
	}()
	app.HandleFunc("GET /x", func(http.ResponseWriter, *http.Request) {})
}

func TestRoute_panicsOnHandleStaticCollision(t *testing.T) {
	t.Parallel()

	app := via.New()
	app.HandleStatic("/static/", fstest.MapFS{})

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on HandleStatic colliding with another HandleStatic")
		}
	}()
	app.HandleStatic("/static/", fstest.MapFS{})
}

func TestRoute_panicsOnGroupHandleFuncDuplicate(t *testing.T) {
	t.Parallel()

	app := via.New()
	g := app.Group("/api")
	g.HandleFunc("/users", func(http.ResponseWriter, *http.Request) {})

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate group route")
		}
	}()
	g.HandleFunc("/users", func(http.ResponseWriter, *http.Request) {})
}
