package via_test

import (
	"net/http/httptest"
	"testing"

	"github.com/go-via/via/internal/viaold"
	"github.com/go-via/via/internal/viaold/h"
	"github.com/stretchr/testify/assert"
)

func TestLayout_wrapsPageContent(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Layout(func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Class("layout"),
				h.Nav(h.Text("nav")),
				h.Main(cmp.Content(ctx)),
			)
		})
	})
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Text("page content"))
		})
	})
	t.Cleanup(server.Close)

	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "nav")
	assert.Contains(t, body, "page content")
}

func TestLayout_groupLayoutReplacesAppLayout(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Layout(func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Text("app-layout"), h.Main(cmp.Content(ctx)))
		})
	})

	g := v.Group("/admin")
	g.Layout(func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Text("admin-layout"), h.Main(cmp.Content(ctx)))
		})
	})
	g.Page("/dashboard", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("dashboard")) })
	})

	v.Page("/home", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("home page")) })
	})
	t.Cleanup(server.Close)

	// Admin uses admin layout
	adminBody := getPageBody(t, server, "/admin/dashboard")
	assert.Contains(t, adminBody, "admin-layout")
	assert.Contains(t, adminBody, "dashboard")
	assert.NotContains(t, adminBody, "app-layout")

	// Home uses app layout
	homeBody := getPageBody(t, server, "/home")
	assert.Contains(t, homeBody, "app-layout")
	assert.Contains(t, homeBody, "home page")
	assert.NotContains(t, homeBody, "admin-layout")
}

func TestLayout_nilRemovesLayout(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Layout(func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Text("layout-wrapper"), h.Main(cmp.Content(ctx)))
		})
	})

	g := v.Group("/bare")
	g.Layout(nil)
	g.Page("/page", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("bare page")) })
	})
	t.Cleanup(server.Close)

	body := getPageBody(t, server, "/bare/page")
	assert.Contains(t, body, "bare page")
	assert.NotContains(t, body, "layout-wrapper")
}

func TestLayout_contentPanicsOnNonLayout(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		v := via.New()
		v.Page("/", func(cmp *via.Cmp) {
			cmp.View(func(ctx *via.Ctx) h.H {
				return h.Div(cmp.Content(ctx))
			})
		})
	})
}

func TestLayout_layoutActionsWork(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Layout(func(cmp *via.Cmp) {
		act := cmp.Action(func(ctx *via.Ctx) error { return nil })
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Button(act.OnClick()), h.Main(cmp.Content(ctx)))
		})
	})
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("page")) })
	})
	t.Cleanup(server.Close)

	body := getPageBody(t, server, "/")
	// Layout action should produce a data-on:click attribute
	assert.Contains(t, body, "data-on:click")
	assert.Contains(t, body, "page")
}

func TestLayout_pageStateAndLayoutCoexist(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Layout(func(cmp *via.Cmp) {
		layoutCount := via.State(cmp, 42)
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.Textf("layout:%d", layoutCount.Get(ctx)),
				h.Main(cmp.Content(ctx)),
			)
		})
	})
	v.Page("/", func(cmp *via.Cmp) {
		pageCount := via.State(cmp, 7)
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("page:%d", pageCount.Get(ctx)))
		})
	})
	t.Cleanup(server.Close)

	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "layout:42")
	assert.Contains(t, body, "page:7")
}
