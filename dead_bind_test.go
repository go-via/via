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

// ddClobberPage reproduces the silent dead-bind footgun that survives the
// Mount-time by-value ban: OnInit swaps the child pointer for a fresh
// struct, orphaning the runtime's by-address handle binding — the page
// renders once but client bindings go dead afterward.
type ddClobberPage struct {
	Child *ddChild
}

func (p *ddClobberPage) OnInit(ctx *via.Ctx) error { p.Child = &ddChild{}; return nil }
func (p *ddClobberPage) View(ctx *via.CtxR) h.H    { return h.Div() }

// ddGoodPage seeds nothing — its bindings stay intact.
type ddGoodPage struct {
	Child *ddChild
}

func (p *ddGoodPage) View(ctx *via.CtxR) h.H { return h.Div() }

// ddByValuePage holds its child composition by value — a registration
// mistake caught at Mount, before any render can mis-bind.
type ddByValuePage struct {
	Child ddChild
}

func (p *ddByValuePage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestMount_panicsOnByValueChildComposition(t *testing.T) {
	t.Parallel()

	app := via.New()
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "Mount with a by-value child composition must panic")
		msg, _ := rec.(string)
		assert.Contains(t, msg, "ddByValuePage.Child",
			"panic must name the offending field")
		assert.Contains(t, msg, "declare the child as a pointer: `Child *ddChild`",
			"panic must state the fix verbatim so the user can paste it in")
	}()
	via.Mount[ddByValuePage](app, "/")
}

// A child-pointer clobber must be caught loudly at render BY DEFAULT —
// silently producing dead client bindings that fail only later is the footgun;
// the check is cheap (amortized once per descriptor) so it's on without a flag.
func TestDeadBind_caughtByDefault(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[ddClobberPage](app, "/")

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"a child-pointer clobber must fail the render loudly by default")
}

// WithoutDevChecks is the escape hatch: disable the check (e.g. if it ever
// false-positives) and the buggy page renders as it did before.
func TestDeadBind_withoutDevChecksRenders(t *testing.T) {
	t.Parallel()

	app := via.New(via.WithoutDevChecks())
	server := vt.Serve(t, app)
	via.Mount[ddClobberPage](app, "/")

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"WithoutDevChecks disables the check — the render is unaffected")
}

// The default check must not false-positive on a correctly-bound composition.
func TestDeadBind_intactBindingsPass(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[ddGoodPage](app, "/")

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"an intact composition must render cleanly under the default check")
}
