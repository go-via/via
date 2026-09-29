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
		h.Button(on.Click(on.Bind(b.Pick, 7))),
		h.Input(on.Input(b.Hit, on.WithDebounce(250*time.Millisecond), on.WithPrevent())),
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

func TestBind_carriesTheArgToTheHandler(t *testing.T) {
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
		{"debounce", on.InputCS(n.Add(1), on.WithDebounce(250*time.Millisecond)), `data-on:input__debounce.250ms=`},
		{"debounce seconds", on.InputCS(n.Add(1), on.WithDebounce(2*time.Second)), `data-on:input__debounce.2000ms=`},
		{"debounce sub-ms rounds up", on.InputCS(n.Add(1), on.WithDebounce(1500*time.Microsecond)), `data-on:input__debounce.2ms=`},
		{"debounce 1µs is 1ms", on.InputCS(n.Add(1), on.WithDebounce(time.Microsecond)), `data-on:input__debounce.1ms=`},
		{"throttle sub-ms rounds up", on.ScrollCS(n.Add(1), on.WithThrottle(time.Nanosecond)), `data-on:scroll__throttle.1ms=`},
		{"throttle", on.ScrollCS(n.Add(1), on.WithThrottle(100*time.Millisecond)), `data-on:scroll__throttle.100ms=`},
		{"once", on.ClickCS(n.Add(1), on.WithOnce()), `data-on:click__once=`},
		{"prevent", on.SubmitCS(n.Add(1), on.WithPrevent()), `data-on:submit__prevent=`},
		{"stop", on.ClickCS(n.Add(1), on.WithStop()), `data-on:click__stop=`},
		{"outside", on.ClickCS(n.Add(1), on.WithOutside()), `data-on:click__outside=`},
		{"window", on.KeydownCS(n.Add(1), on.WithWindow()), `data-on:keydown__window=`},
		{"chained in fixed order", on.ClickCS(n.Add(1), on.WithWindow(), on.WithOnce(), on.WithThrottle(time.Second)), `data-on:click__throttle.1000ms__once__window=`},
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
	all := []on.Option{on.WithDebounce(1500 * time.Microsecond), on.WithThrottle(time.Second), on.WithOnce(), on.WithPrevent(), on.WithStop(), on.WithOutside(), on.WithWindow()}
	tests := []struct {
		name  string
		event string
		opts  []on.Option
		want  string
	}{
		{"debounce", "input", []on.Option{on.WithDebounce(500 * time.Millisecond)}, `data-on:input__debounce.500ms=`},
		{"debounce sub-ms rounds up", "input", []on.Option{on.WithDebounce(1500 * time.Microsecond)}, `data-on:input__debounce.2ms=`},
		{"debounce 1µs is 1ms", "input", []on.Option{on.WithDebounce(time.Microsecond)}, `data-on:input__debounce.1ms=`},
		{"throttle", "scroll", []on.Option{on.WithThrottle(time.Second)}, `data-on:scroll__throttle.1000ms=`},
		{"once", "click", []on.Option{on.WithOnce()}, `data-on:click__once=`},
		{"prevent", "submit", []on.Option{on.WithPrevent()}, `data-on:submit__prevent=`},
		{"stop", "click", []on.Option{on.WithStop()}, `data-on:click__stop=`},
		{"outside", "click", []on.Option{on.WithOutside()}, `data-on:click__outside=`},
		{"window", "keydown", []on.Option{on.WithWindow()}, `data-on:keydown__window=`},
		{"all on a namespaced event", "via:patch", all, `data-on:via:patch__debounce.2ms__throttle.1000ms__once__prevent__stop__outside__window=`},
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

func TestWithModifier_appendsTheRawModifierToTheEventKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		event string
		opts  []on.Option
		want  string
	}{
		{"with tags", "click", []on.Option{on.WithModifier("delay.300ms")}, `data-on:click__delay.300ms=`},
		{"bare", "scroll", []on.Option{on.WithModifier("passive")}, `data-on:scroll__passive=`},
		{"after typed options, in option order", "keydown",
			[]on.Option{on.WithModifier("viewtransition"), on.WithWindow(), on.WithModifier("delay.1s")},
			`data-on:keydown__window__viewtransition__delay.1s=`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var page string
			require.NotPanics(t, func() {
				_, page = vt.Serve(t, via.Handler(modPage{event: tt.event, opts: tt.opts})).Get("/")
			})
			require.Contains(t, page, tt.want)
			require.Contains(t, renderCS(t, on.EventCS(tt.event, expr.Expr("$n").Add(1), tt.opts...)), tt.want)
		})
	}
}

func TestWithModifier_panicsOnAnInvalidModifier(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "Delay", "delay__once", "__delay", `delay"`, "delay 1", "delay.", ".delay", "delay..1", "delay.3-00", "delay.1.5ms'"} {
		require.Panics(t, func() { on.WithModifier(raw) }, raw)
	}
}

func TestWithModifier_panicsOnAModeledModifierNamingTheTypedOption(t *testing.T) {
	t.Parallel()
	for raw, typed := range map[string]string{
		"debounce.300ms": "WithDebounce", "throttle.1s": "WithThrottle", "once": "WithOnce",
		"outside": "WithOutside", "prevent": "WithPrevent", "stop": "WithStop", "window": "WithWindow",
	} {
		require.PanicsWithValue(t, `on: WithModifier("`+raw+`"): use on.`+typed, func() { on.WithModifier(raw) }, raw)
	}
}

func TestWithDebounce_panicsOnANonPositiveDuration(t *testing.T) {
	t.Parallel()
	assert.PanicsWithValue(t, "on: WithDebounce needs a positive duration, got 0s", func() { on.WithDebounce(0) })
	assert.PanicsWithValue(t, "on: WithThrottle needs a positive duration, got -1s", func() { on.WithThrottle(-time.Second) })
}

func TestWithDebounce_panicsOnAConflictingRepeat(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		on.ClickCS(expr.Expr("$n").Add(1), on.WithDebounce(time.Second), on.WithDebounce(2*time.Second))
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
