package via_test

import (
	"errors"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAction_onClickRendersDataOnClick(t *testing.T) {
	act := captureAction(func(c *via.Context) actionT {
		return c.Action(func() error { return nil })
	})
	out := renderH(t, h.Button(act.OnClick()))
	assert.Contains(t, out, "data-on:click")
	assert.Contains(t, out, "/_action/")
}

func TestAction_onChangeRendersDataOnChange(t *testing.T) {
	act := captureAction(func(c *via.Context) actionT {
		return c.Action(func() error { return nil })
	})
	out := renderH(t, h.Input(act.OnChange()))
	assert.Contains(t, out, "data-on:change")
	assert.Contains(t, out, "debounce")
}

func TestAction_onKeyDownRendersKeyCondition(t *testing.T) {
	act := captureAction(func(c *via.Context) actionT {
		return c.Action(func() error { return nil })
	})
	out := renderH(t, h.Input(act.OnKeyDown("Enter")))
	assert.Contains(t, out, "data-on:keydown")
	assert.Contains(t, out, "Enter")
	assert.Contains(t, out, "evt.key")
}

func TestAction_actionWithSetSignalSetsValueBeforeAction(t *testing.T) {
	v := via.New()
	var out string
	v.Page("/", func(c *via.Context) {
		sig := via.Signal(c, "initial")
		act := c.Action(func() error { return nil })
		node := h.Button(act.OnClick(via.ActionWithSetSignal(sig, "clicked")))
		out = renderH(t, node)
		c.View(func() h.H { return h.Div() })
	})
	assert.Contains(t, out, "$")
	assert.Contains(t, out, "clicked")
	assert.Contains(t, out, "/_action/")
}

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

func TestAction_errorReturnsAlert(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		act := c.Action(func() error {
			return errors.New("test error")
		})
		c.View(func() h.H {
			return h.Div(act.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	readSSEEvent(t, stream, sseTimeout)
	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Contains(t, ev.data, "alert")
	assert.Contains(t, ev.data, "test error")
}

func TestAction_panicShowsGenericAlert(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		act := c.Action(func() error {
			panic("boom")
		})
		c.View(func() h.H {
			return h.Div(act.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	readSSEEvent(t, stream, sseTimeout)
	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Contains(t, ev.data, "alert")
	assert.Contains(t, ev.data, "Something went wrong")
}

func TestAction_autoSyncsAfterExecution(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		s := via.State(c, 0)
		act := c.Action(func() error {
			s.Set(c, 42)
			return nil
			// no c.Sync() — relying on autoSync
		})
		c.View(func() h.H {
			return h.Div(h.Textf("val=%d", s.Get(c)), act.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	// Drain initial connection event.
	readSSEEvent(t, stream, sseTimeout)

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "val=42")
}

func TestAction_autoSyncCalledAfterAction(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(c *via.Context) {
		s := via.State(c, 0)
		act := c.Action(func() error {
			s.Set(c, 42)
			return nil
		})
		c.View(func() h.H {
			return h.Div(
				h.Textf("val=%d", s.Get(c)),
				act.OnClick(),
			)
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	readSSEEvent(t, stream, sseTimeout)

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "val=42")
}

func TestAction_mutatingStateAndSignalProducesTwoPatches(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(c *via.Context) {
		s := via.State(c, 0)
		sig := via.Signal(c, "initial")

		act := c.Action(func() error {
			s.Set(c, 10)
			sig.SetValue("modified")
			return nil
		})

		c.View(func() h.H {
			return h.Div(
				h.Textf("state=%d", s.Get(c)),
				h.Textf("signal=%s", sig.Get(c)),
				act.OnClick(),
			)
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	initialEv := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", initialEv.eventType)

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "state=10")

	sigEv := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-signals", sigEv.eventType)
}

func TestAction_mutatingTwoStatesAndTwoSignalsProducesTwoPatches(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(c *via.Context) {
		stateA := via.State(c, 0)
		stateB := via.State(c, 0)
		sigA := via.Signal(c, "a")
		sigB := via.Signal(c, "b")

		act := c.Action(func() error {
			stateA.Set(c, 1)
			stateB.Set(c, 2)
			sigA.SetValue("modified-a")
			sigB.SetValue("modified-b")
			return nil
		})

		c.View(func() h.H {
			return h.Div(
				h.Textf("a=%d", stateA.Get(c)),
				h.Textf("b=%d", stateB.Get(c)),
				h.Textf("sigA=%s", sigA.Get(c)),
				h.Textf("sigB=%s", sigB.Get(c)),
				act.OnClick(),
			)
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	readSSEEvent(t, stream, sseTimeout)

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "a=1")
	assert.Contains(t, ev.data, "b=2")

	sigEv := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-signals", sigEv.eventType)
}
