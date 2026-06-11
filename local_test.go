package via_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type localSignalPage struct{}

func (p *localSignalPage) View(ctx *via.CtxR) h.H {
	open := via.Local("open")
	return h.Div(
		open.Init(false),
		h.Button(open.Toggle(), h.Text("menu")),
		h.Nav(open.Show(), h.Text("links")),
		h.Div(open.ShowUnless(), h.Text("placeholder")),
		h.Span(open.Class("active"), open.Text()),
		h.Input(open.Bind()),
	)
}

func TestLocal_rendersClientOnlySignalHelpers(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[localSignalPage](app, "/")

	body := getBody(t, server, "/")
	cases := []struct{ name, needle string }{
		{"init declares _-prefixed signal", `data-signals="{_open:false}"`},
		{"toggle flips the local signal", `data-on:click="$_open=!$_open"`},
		{"show binds to $_open", `data-show="$_open"`},
		{"show-unless negates", `data-show="!$_open"`},
		{"class toggles by truthiness", `data-class:active="$_open"`},
		{"text binds value", `data-text="$_open"`},
		{"bind uses bare _name", `data-bind="_open"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, body, c.needle, c.name)
		})
	}
}

// TestLocal_RefReturnsDollarExpression locks Ref's contract: it yields the
// "$_name" expression used inside raw Datastar expressions — the same
// reference Show/Text/Toggle build on, exposed for hand-written guards.
func TestLocal_RefReturnsDollarExpression(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "$_open", via.Local("open").Ref(),
		"Ref must return the $_-prefixed client-only signal reference")
}

// TestLocal_InitPanicsOnNonJSONValue guards the loud-fail contract: an
// un-encodable initial value (chan/func) is a programmer error and must
// surface as a panic at first render, not emit a broken data-signals attr.
func TestLocal_InitPanicsOnNonJSONValue(t *testing.T) {
	t.Parallel()
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "Init must panic when the value cannot be JSON-encoded")
		msg, ok := rec.(string)
		require.True(t, ok, "panic value should be a string, got %T", rec)
		assert.Contains(t, msg, "via.Local(open).Init:",
			"panic message should name the signal + call site for grep-ability")
		assert.Contains(t, msg, "cannot be JSON-encoded",
			"panic message should explain the failure mode")
	}()
	via.Local("open").Init(make(chan int))
}
