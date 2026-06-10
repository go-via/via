package via_test

import (
	"net/http"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ddChild struct {
	Count via.Signal[int]
}

func (c *ddChild) View(ctx *via.CtxR) h.H { return h.Div() }

// ddClobberPage reproduces the silent dead-bind footgun: OnInit replaces the
// child composition by value, which zeroes the runtime's by-address handle
// binding — the page renders once but client bindings go dead afterward.
type ddClobberPage struct {
	Child ddChild
}

func (p *ddClobberPage) OnInit(ctx *via.Ctx) error { p.Child = ddChild{}; return nil }
func (p *ddClobberPage) View(ctx *via.CtxR) h.H    { return h.Div() }

// ddGoodPage seeds nothing by value — its bindings stay intact.
type ddGoodPage struct {
	Child ddChild
}

func (p *ddGoodPage) View(ctx *via.CtxR) h.H { return h.Div() }

// With WithDevChecks a by-value child clobber must be caught loudly at render
// instead of silently producing dead client bindings that fail only later.
func TestDeadBind_devChecksCatchByValueClobber(t *testing.T) {
	t.Parallel()

	app := via.New(via.WithDevChecks())
	server := vt.Serve(t, app)
	via.Mount[ddClobberPage](app, "/")

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"a by-value child clobber under dev checks must fail the render loudly")
}

// Without the dev flag, production pays nothing — the (buggy) page still
// renders, matching today's behavior.
func TestDeadBind_offByDefault(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[ddClobberPage](app, "/")

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"without WithDevChecks the render is unaffected (prod pays nothing)")
}

// Dev checks must not false-positive on a correctly-bound composition.
func TestDeadBind_devChecksPassIntactBindings(t *testing.T) {
	t.Parallel()

	app := via.New(via.WithDevChecks())
	server := vt.Serve(t, app)
	via.Mount[ddGoodPage](app, "/")

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"an intact composition must render cleanly under dev checks")
}
