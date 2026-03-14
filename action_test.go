package via_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This guards against breaking the client-side event binding contract.
func TestAction_onClickRendersDataOnClick(t *testing.T) {
	act := captureAction(func(c *via.Context) actionT {
		return c.Action(func() {})
	})
	out := renderH(t, h.Button(act.OnClick()))
	assert.Contains(t, out, "data-on:click")
	assert.Contains(t, out, "/_action/")
}

// This guards against accidentally removing debounce and causing excessive server calls.
func TestAction_onChangeRendersDataOnChange(t *testing.T) {
	act := captureAction(func(c *via.Context) actionT {
		return c.Action(func() {})
	})
	out := renderH(t, h.Input(act.OnChange()))
	assert.Contains(t, out, "data-on:change")
	assert.Contains(t, out, "debounce")
}

// This guards against OnKeyDown firing on every keypress instead of only the intended key.
func TestAction_onKeyDownRendersKeyCondition(t *testing.T) {
	act := captureAction(func(c *via.Context) actionT {
		return c.Action(func() {})
	})
	out := renderH(t, h.Input(act.OnKeyDown("Enter")))
	assert.Contains(t, out, "data-on:keydown")
	assert.Contains(t, out, "Enter")
	assert.Contains(t, out, "evt.key")
}

// TestAction_actionWithSetSignalSetsValueBeforeAction verifies ActionWithSetSignal prepends a signal assignment before the action call.
// This guards against signal values being stale when the action handler runs.
func TestAction_actionWithSetSignalSetsValueBeforeAction(t *testing.T) {
	v := via.New()
	var out string
	v.Page("/", func(c *via.Context) {
		sig := via.Signal(c, "initial")
		act := c.Action(func() {})
		node := h.Button(act.OnClick(via.ActionWithSetSignal(sig, "clicked")))
		out = renderH(t, node)
		c.View(func() h.H { return h.Div() })
	})
	assert.Contains(t, out, "$")
	assert.Contains(t, out, "clicked")
	assert.Contains(t, out, "/_action/")
}

// TestAction_nilFuncReturnsNil verifies c.Action(nil) returns nil without panicking.
// This guards against accidental nil-pointer dereferences when nil actions are placed in views.
func TestAction_nilFuncReturnsNil(t *testing.T) {
	v := via.New()
	require.NotPanics(t, func() {
		v.Page("/", func(c *via.Context) {
			act := c.Action(nil)
			assert.Nil(t, act)
			c.View(func() h.H { return h.Div() })
		})
	})
}
