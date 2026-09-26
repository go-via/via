package on_test

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
)

type board struct{ got string }

func (b *board) Hit(ctx *via.Ctx)          { b.got = "hit" }
func (b *board) Pick(ctx *via.Ctx, id int) { b.got = "picked " + strconv.Itoa(id) }
func (b *board) View() h.H {
	return h.Div(
		h.Button(on.Click(b.Hit)),
		h.Button(on.Click(on.WithArg(b.Pick, 7))),
		h.Input(on.Input(b.Hit, on.Debounce(250*time.Millisecond), on.Prevent())),
		h.Div(on.Event("pointerdown", b.Hit)),
		h.P(h.Str(b.got)),
	)
}

func TestClick_wiresAPostAction(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(board{}))
	status, page := app.Get("/")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, `data-on:click="@post('`)

	_, body := app.Action(0).Fire()
	assert.Contains(t, body, "<p>hit</p>")
}

func TestWithArg_carriesTheArgToTheHandler(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(board{}))
	_, body := app.Action(1).Fire()
	assert.Contains(t, body, "<p>picked 7</p>")
}

func TestInput_appendsModifiersToTheServerAction(t *testing.T) {
	t.Parallel()
	_, page := vt.Serve(t, via.Handler(board{})).Get("/")
	assert.Contains(t, page, `data-on:input__debounce.250ms__prevent="@post('`)
}

func TestEvent_wiresANamedEvent(t *testing.T) {
	t.Parallel()
	_, page := vt.Serve(t, via.Handler(board{})).Get("/")
	assert.Contains(t, page, `data-on:pointerdown="@post('`)
}

type csPage struct{ attr h.Attr }

func (p *csPage) View() h.H { return h.Div(p.attr) }

func renderCS(t *testing.T, attr h.Attr) string {
	t.Helper()
	status, page := vt.Serve(t, via.Handler(csPage{attr: attr})).Get("/")
	require.Equal(t, http.StatusOK, status)
	return page
}

func TestCS_emitsTheEventAndModifiers(t *testing.T) {
	t.Parallel()
	n := expr.Expr("$n")
	tests := []struct {
		name string
		attr h.Attr
		want string
	}{
		{"click", on.ClickCS(n.Add(1)), `data-on:click="$n += 1"`},
		{"dblclick", on.DblClickCS(n.Add(1)), `data-on:dblclick="$n += 1"`},
		{"input", on.InputCS(n.Add(1)), `data-on:input="$n += 1"`},
		{"change", on.ChangeCS(n.Add(1)), `data-on:change="$n += 1"`},
		{"submit", on.SubmitCS(n.Add(1)), `data-on:submit="$n += 1"`},
		{"keydown", on.KeydownCS(n.Add(1)), `data-on:keydown="$n += 1"`},
		{"keyup", on.KeyupCS(n.Add(1)), `data-on:keyup="$n += 1"`},
		{"focus", on.FocusCS(n.Add(1)), `data-on:focus="$n += 1"`},
		{"blur", on.BlurCS(n.Add(1)), `data-on:blur="$n += 1"`},
		{"load", on.LoadCS(n.Add(1)), `data-on:load="$n += 1"`},
		{"mouseenter", on.MouseEnterCS(n.Add(1)), `data-on:mouseenter="$n += 1"`},
		{"mouseleave", on.MouseLeaveCS(n.Add(1)), `data-on:mouseleave="$n += 1"`},
		{"scroll", on.ScrollCS(n.Add(1)), `data-on:scroll="$n += 1"`},
		{"event", on.EventCS("pointerdown", n.Add(1)), `data-on:pointerdown="$n += 1"`},
		{"debounce", on.InputCS(n.Add(1), on.Debounce(250*time.Millisecond)), `data-on:input__debounce.250ms=`},
		{"debounce seconds", on.InputCS(n.Add(1), on.Debounce(2*time.Second)), `data-on:input__debounce.2000ms=`},
		{"debounce sub-ms", on.InputCS(n.Add(1), on.Debounce(1500*time.Microsecond)), `data-on:input__debounce.1.5ms=`},
		{"throttle", on.ScrollCS(n.Add(1), on.Throttle(100*time.Millisecond)), `data-on:scroll__throttle.100ms=`},
		{"once", on.ClickCS(n.Add(1), on.Once()), `data-on:click__once=`},
		{"prevent", on.SubmitCS(n.Add(1), on.Prevent()), `data-on:submit__prevent=`},
		{"stop", on.ClickCS(n.Add(1), on.Stop()), `data-on:click__stop=`},
		{"outside", on.ClickCS(n.Add(1), on.Outside()), `data-on:click__outside=`},
		{"window", on.KeydownCS(n.Add(1), on.Window()), `data-on:keydown__window=`},
		{"chained in fixed order", on.ClickCS(n.Add(1), on.Window(), on.Once(), on.Throttle(time.Second)), `data-on:click__throttle.1000ms__once__window=`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, renderCS(t, tt.attr), tt.want)
		})
	}
}

type modPage struct {
	event string
	opts  []on.Option
}

func (p *modPage) Hit(ctx *via.Ctx) {}
func (p *modPage) View() h.H        { return h.Div(on.Event(p.event, p.Hit, p.opts...)) }

func TestEvent_rendersEveryModifierOnTheServerAction(t *testing.T) {
	t.Parallel()
	all := []on.Option{on.Debounce(1500 * time.Microsecond), on.Throttle(time.Second), on.Once(), on.Prevent(), on.Stop(), on.Outside(), on.Window()}
	tests := []struct {
		name  string
		event string
		opts  []on.Option
		want  string
	}{
		{"debounce", "input", []on.Option{on.Debounce(500 * time.Millisecond)}, `data-on:input__debounce.500ms=`},
		{"debounce sub-ms", "input", []on.Option{on.Debounce(1500 * time.Microsecond)}, `data-on:input__debounce.1.5ms=`},
		{"throttle", "scroll", []on.Option{on.Throttle(time.Second)}, `data-on:scroll__throttle.1000ms=`},
		{"once", "click", []on.Option{on.Once()}, `data-on:click__once=`},
		{"prevent", "submit", []on.Option{on.Prevent()}, `data-on:submit__prevent=`},
		{"stop", "click", []on.Option{on.Stop()}, `data-on:click__stop=`},
		{"outside", "click", []on.Option{on.Outside()}, `data-on:click__outside=`},
		{"window", "keydown", []on.Option{on.Window()}, `data-on:keydown__window=`},
		{"all on a namespaced event", "via:patch", all, `data-on:via:patch__debounce.1.5ms__throttle.1000ms__once__prevent__stop__outside__window=`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var page string
			require.NotPanics(t, func() {
				_, page = vt.Serve(t, via.Handler(modPage{event: tt.event, opts: tt.opts})).Get("/")
			})
			assert.Contains(t, page, tt.want)
		})
	}
}

func TestDebounce_panicsOnANonPositiveDuration(t *testing.T) {
	t.Parallel()
	assert.PanicsWithValue(t, "on: Debounce needs a positive duration, got 0s", func() { on.Debounce(0) })
	assert.PanicsWithValue(t, "on: Throttle needs a positive duration, got -1s", func() { on.Throttle(-time.Second) })
}

func TestDebounce_panicsOnAConflictingRepeat(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		on.ClickCS(expr.Expr("$n").Add(1), on.Debounce(time.Second), on.Debounce(2*time.Second))
	})
}

var (
	validNames   = []string{"click", "pointerdown", "via:patch", "via:remove", "my-event", "a.b", "h1", "x-y:z.w"}
	invalidNames = []string{"", "Click", "DOMContentLoaded", `click" onload=x`, "my event", "click__once", "1click", "-x", "x-", "x--y", "x:", "via::patch", "my_event"}
)

func TestEvent_acceptsLowerCaseEventNames(t *testing.T) {
	t.Parallel()
	for _, name := range validNames {
		assert.NotPanics(t, func() { on.Event(name, func(*via.Ctx) {}) }, name)
	}
}

func TestEvent_panicsOnAnInvalidName(t *testing.T) {
	t.Parallel()
	for _, name := range invalidNames {
		assert.Panics(t, func() { on.Event(name, func(*via.Ctx) {}) }, name)
	}
}

func TestEventCS_acceptsLowerCaseEventNames(t *testing.T) {
	t.Parallel()
	for _, name := range validNames {
		assert.NotPanics(t, func() { on.EventCS(name, expr.Expr("$n").Add(1)) }, name)
	}
}

func TestEventCS_panicsOnAnInvalidName(t *testing.T) {
	t.Parallel()
	for _, name := range invalidNames {
		assert.Panics(t, func() { on.EventCS(name, expr.Expr("$n").Add(1)) }, name)
	}
}

func TestEventCS_namesTheBadInputInThePanic(t *testing.T) {
	t.Parallel()
	assert.PanicsWithValue(t,
		`on: event name "click__once" must be lower-case letters and digits, optionally joined by ':', '.' or '-' (such as "click" or "via:patch")`,
		func() { on.EventCS("click__once", expr.Expr("$n").Add(1)) })
}
