package via_test

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

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
