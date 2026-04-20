package via_test

import (
	"errors"
	"testing"
	"time"

	"github.com/go-via/via/internal/viaold"
	"github.com/go-via/via/internal/viaold/h"
	"github.com/stretchr/testify/assert"
)

func TestAction_onClickRendersDataOnClick(t *testing.T) {
	t.Parallel()

	act := captureAction(func(cmp *via.Cmp) actionT {
		return cmp.Action(func(ctx *via.Ctx) error { return nil })
	})
	out := renderH(t, h.Button(act.OnClick()))
	assert.Contains(t, out, "data-on:click")
	assert.Contains(t, out, "/_action/")
}

func TestAction_onChangeRendersDataOnChange(t *testing.T) {
	t.Parallel()

	act := captureAction(func(cmp *via.Cmp) actionT {
		return cmp.Action(func(ctx *via.Ctx) error { return nil })
	})
	out := renderH(t, h.Input(act.OnChange()))
	assert.Contains(t, out, "data-on:change")
	assert.Contains(t, out, "debounce")
}

func TestAction_onKeyDownRendersKeyCondition(t *testing.T) {
	t.Parallel()

	act := captureAction(func(cmp *via.Cmp) actionT {
		return cmp.Action(func(ctx *via.Ctx) error { return nil })
	})
	out := renderH(t, h.Input(act.OnKeyDown("Enter")))
	assert.Contains(t, out, "data-on:keydown")
	assert.Contains(t, out, "Enter")
	assert.Contains(t, out, "evt.key")
}

func TestAction_actionWithSetSignalSetsValueBeforeAction(t *testing.T) {
	t.Parallel()

	v := via.New()
	var out string
	v.Page("/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, "initial")
		act := cmp.Action(func(ctx *via.Ctx) error { return nil })
		node := h.Button(act.OnClick(via.ActionWithSetSignal(sig, "clicked")))
		out = renderH(t, node)
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	assert.Contains(t, out, "$")
	assert.Contains(t, out, "clicked")
	assert.Contains(t, out, "/_action/")
}

func TestAction_panicsOnNilHandler(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		v := via.New()
		v.Page("/", func(cmp *via.Cmp) {
			cmp.Action(nil)
			cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
		})
	})
}

func TestAction_errorReturnsAlert(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		act := cmp.Action(func(ctx *via.Ctx) error {
			return errors.New("test error")
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(act.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Contains(t, ev.data, "alert")
	assert.Contains(t, ev.data, "test error")
}

func TestAction_panicShowsGenericAlert(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		act := cmp.Action(func(ctx *via.Ctx) error {
			panic("boom")
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(act.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Contains(t, ev.data, "alert")
	assert.Contains(t, ev.data, "Something went wrong")
}

func TestAction_autoSyncsAfterExecution(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0)
		act := cmp.Action(func(ctx *via.Ctx) error {
			s.Set(ctx, 42)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("val=%d", s.Get(ctx)), act.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "val=42")
}

func TestAction_autoSyncPatchIncludesIDSoDatastarCanMorph(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0)
		act := cmp.Action(func(ctx *via.Ctx) error {
			s.Set(ctx, 1)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("val=%d", s.Get(ctx)), act.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Contains(t, ev.data, "id=", "auto-sync patch must include an id attribute for Datastar DOM morphing")
}

func TestAction_autoSyncCalledAfterAction(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0)
		act := cmp.Action(func(ctx *via.Ctx) error {
			s.Set(ctx, 42)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.Textf("val=%d", s.Get(ctx)),
				act.OnClick(),
			)
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "val=42")
}

func TestAction_mutatingStateAndSignalProducesTwoPatches(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0)
		sig := via.Signal(cmp, "initial")

		act := cmp.Action(func(ctx *via.Ctx) error {
			s.Set(ctx, 10)
			sig.SetValue(ctx, "modified")
			return nil
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.Textf("state=%d", s.Get(ctx)),
				h.Textf("signal=%s", sig.Get(ctx)),
				act.OnClick(),
			)
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

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

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		stateA := via.State(cmp, 0)
		stateB := via.State(cmp, 0)
		sigA := via.Signal(cmp, "a")
		sigB := via.Signal(cmp, "b")

		act := cmp.Action(func(ctx *via.Ctx) error {
			stateA.Set(ctx, 1)
			stateB.Set(ctx, 2)
			sigA.SetValue(ctx, "modified-a")
			sigB.SetValue(ctx, "modified-b")
			return nil
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.Textf("a=%d", stateA.Get(ctx)),
				h.Textf("b=%d", stateB.Get(ctx)),
				h.Textf("sigA=%s", sigA.Get(ctx)),
				h.Textf("sigB=%s", sigB.Get(ctx)),
				act.OnClick(),
			)
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "a=1")
	assert.Contains(t, ev.data, "b=2")

	sigEv := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-signals", sigEv.eventType)
}
