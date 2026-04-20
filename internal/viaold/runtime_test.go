package via_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- SSE tests ---

func TestSSE_reconnectAfterDisconnect(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		n := via.State(cmp, 0)
		act := cmp.Action(func(ctx *via.Ctx) error {
			n.Set(ctx, n.Get(ctx)+1)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("n=%d", n.Get(ctx)), act.OnClick())
		})
	})
	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	_, cancel1 := connectSSE(t, server, ctxID)

	cancel1()
	time.Sleep(50 * time.Millisecond)

	stream2, cancel2 := connectSSE(t, server, ctxID)
	defer cancel2()

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream2, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "n=1")
}

func TestSSE_establishesConnection(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		n := via.State(cmp, 0)
		act := cmp.Action(func(ctx *via.Ctx) error {
			n.Set(ctx, n.Get(ctx)+1)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("n=%d", n.Get(ctx)), act.OnClick())
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
	assert.Contains(t, ev.data, "n=1")
}

func TestSSE_elementPatchUsesDatastarWireFormat(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		syncAct := cmp.Action(func(ctx *via.Ctx) error {
			ctx.SyncElements(h.Div(h.ID("box"), h.Text("updated")))
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.Div(h.ID("box"), h.Text("initial")),
				syncAct.OnClick(),
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
	assert.Contains(t, ev.data, "elements <div", "element patch must use Datastar 'elements' data prefix")
}

func TestSSE_execScriptWrapsInScriptTag(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		scriptAct := cmp.Action(func(ctx *via.Ctx) error {
			ctx.ExecScript("console.log('hello')")
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(scriptAct.OnClick())
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
	assert.Contains(t, ev.data, "<script", "ExecScript must wrap content in a script tag")
	assert.Contains(t, ev.data, "console.log")
}

func TestSSE_signalPatchUsesDatastarWireFormat(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0)
		sig := via.Signal(cmp, "initial")

		act := cmp.Action(func(ctx *via.Ctx) error {
			s.Set(ctx, 1)
			sig.SetValue(ctx, "modified")
			return nil
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.Textf("state=%d", s.Get(ctx)),
				h.Input(sig.Bind()),
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

	readSSEEvent(t, stream, sseTimeout) // consume element patch

	sigEv := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-signals", sigEv.eventType)
	assert.Contains(t, sigEv.data, "signals ", "signal patch must use Datastar 'signals' data prefix")
}

func TestSSE_actionTriggersSyncUpdate(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		n := via.State(cmp, 0)
		act := cmp.Action(func(ctx *via.Ctx) error {
			n.Set(ctx, n.Get(ctx)+1)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("n=%d", n.Get(ctx)), act.OnClick())
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
	assert.Contains(t, ev.data, "n=1")
}

func TestSSE_injectedSignalNotEchoedWhenStateMutated(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0)
		sig := via.Signal(cmp, "original")
		act := cmp.Action(func(ctx *via.Ctx) error {
			s.Set(ctx, 1)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.Textf("val=%d", s.Get(ctx)),
				h.Input(sig.Bind()),
				act.OnClick(),
			)
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	sigID := extractSignalID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(20 * time.Millisecond)

	triggerActionWithSignal(t, server.URL, ctxID, actionID, sigID, "fromBrowser")

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "val=1")

	gotSigPatch, sigEv := tryReadEvent(t, stream, 50*time.Millisecond)
	assert.False(t, gotSigPatch, "signal injected from browser must not be echoed back, got: %s %s", sigEv.eventType, sigEv.data)
}

func TestSSE_actionReceivesInjectedSignal(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, "initial")
		act := cmp.Action(func(ctx *via.Ctx) error {
			assert.Equal(t, "injected", sig.Get(ctx))
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.Input(sig.Bind()),
				sig.Text(),
				act.OnClick(),
			)
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	sigID := extractSignalID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(20 * time.Millisecond)

	triggerActionWithSignal(t, server.URL, ctxID, actionID, sigID, "injected")

	got, ev := tryReadEvent(t, stream, 50*time.Millisecond)
	assert.False(t, got, "no SSE event expected when action does not modify state, got: %s %s", ev.eventType, ev.data)
}

func TestSSE_noSignalSyncWhenSignalNotModifiedInAction(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, "initial")
		act := cmp.Action(func(ctx *via.Ctx) error {
			t.Logf("Action read: %s", sig.Get(ctx))
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.Input(sig.Bind()),
				sig.Text(),
				act.OnClick(),
			)
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	sigID := extractSignalID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(20 * time.Millisecond)

	triggerActionWithSignal(t, server.URL, ctxID, actionID, sigID, "injected")

	got, ev := tryReadEvent(t, stream, 50*time.Millisecond)
	assert.False(t, got, "no SSE event expected when signal is not modified in action, got: %s %s", ev.eventType, ev.data)
}

func TestSSE_signalOnlyMutationSendsSignalPatchWithoutElementPatch(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, "initial")
		act := cmp.Action(func(ctx *via.Ctx) error {
			sig.SetValue(ctx, "updated")
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(sig.Text(), act.OnClick())
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
	assert.Equal(t, "datastar-patch-signals", ev.eventType, "signal-only mutation must send signal patch")
	assert.Contains(t, ev.data, "updated")
}

func TestSSE_injectedSignalNotEchoedBack(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, "original")
		act := cmp.Action(func(ctx *via.Ctx) error { return nil })
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Input(sig.Bind()), act.OnChange())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	sigID := extractSignalID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(20 * time.Millisecond)

	triggerActionWithSignal(t, server.URL, ctxID, actionID, sigID, "newvalue")

	got, ev := tryReadEvent(t, stream, 50*time.Millisecond)
	assert.False(t, got, "injected signal must not be echoed back to the browser, got event: %s %s", ev.eventType, ev.data)
}

func TestSSE_noSignalPatchWhenSignalUnchanged(t *testing.T) {
	t.Parallel()

	counter := 0
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, "original")
		act := cmp.Action(func(ctx *via.Ctx) error {
			counter++
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.Textf("val=%s", sig.Get(ctx)),
				act.OnClick(),
			)
		})
	})

	require.Equal(t, 0, counter)

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	gotEvent, ev := tryReadEvent(t, stream, 50*time.Millisecond)

	t.Logf("After action - event: type=%s, data=%s, counter=%d", ev.eventType, ev.data, counter)

	assert.False(t, gotEvent, "no patch should be sent when signal is unchanged")
}

// --- View tests ---

func TestView_rendersInDivWithContextID(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.P(h.Text("content")) })
	})
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, `<div id=`)
	assert.Contains(t, body, "content")
}

// --- Component tests ---

func TestComponent_rendersNestedInView(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		compView := cmp.Component(func(comp *via.Cmp) {
			comp.View(func(ctx *via.Ctx) h.H { return h.Span(h.Text("from-component")) })
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(compView(ctx))
		})
	})
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "from-component")
}

func TestComponent_runsInitOnPageLoad(t *testing.T) {
	t.Parallel()

	initCalled := false
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.Component(func(comp *via.Cmp) {
			comp.Init(func(ctx *via.Ctx) { initCalled = true })
			comp.View(func(ctx *via.Ctx) h.H { return h.Span(h.Text("component")) })
		})
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})

	assert.True(t, initCalled, "init should run when component is created during page load")

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(50 * time.Millisecond)
	assert.True(t, initCalled, "init should persist across SSE connections")
}

func TestComponent_disposeCallback(t *testing.T) {
	t.Parallel()

	disposeCalled := false
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.Dispose(func() { disposeCalled = true })
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("page")) })
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(20 * time.Millisecond)
	assert.False(t, disposeCalled, "dispose should not run before close")

	req, err := http.NewRequest("POST", server.URL+"/_sse/close", bytes.NewBufferString(ctxID))
	require.NoError(t, err)
	resp, err := clientFor(server.URL).Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.True(t, disposeCalled, "dispose should run after session close")
}

// --- Ctx tests ---

func TestGetPathParam_returnsEmptyForMissingParam(t *testing.T) {
	t.Parallel()

	v := via.New()
	var got string
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			got = ctx.GetPathParam("missing")
			return h.Div()
		})
	})
	assert.Equal(t, "", got)
}

func TestCtx_runsInitOnPageLoad(t *testing.T) {
	t.Parallel()

	initRanCount := 0
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.Init(func(ctx *via.Ctx) { initRanCount++ })
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("page")) })
	})

	getPageBody(t, server, "/")
	assert.Equal(t, 1, initRanCount, "init must run on first page load")

	getPageBody(t, server, "/")
	assert.Equal(t, 2, initRanCount, "init must run on every page load")
}

func TestCtx_syncReRendersAndPushesView(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		s := via.State(cmp, "before")

		act := cmp.Action(func(ctx *via.Ctx) error {
			s.Set(ctx, "after")
			ctx.Sync()
			return nil
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("val=%s", s.Get(ctx)), act.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	assert.Contains(t, body, "val=before")

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "val=after", "Sync must re-render and push the updated view")
}

func TestCtx_syncFlushesSignalPatches(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, "original")

		act := cmp.Action(func(ctx *via.Ctx) error {
			sig.SetValue(ctx, "pushed")
			ctx.Sync()
			return nil
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(sig.Text(), act.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	ev1 := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev1.eventType)

	ev2 := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-signals", ev2.eventType)
	assert.Contains(t, ev2.data, "pushed")
}

func TestCtx_marshalAndPatchSignalsPushesArbitrarySignals(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		act := cmp.Action(func(ctx *via.Ctx) error {
			ctx.MarshalAndPatchSignals(map[string]any{
				"_customFlag": true,
				"_theme":      "dark",
			})
			return nil
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
	assert.Equal(t, "datastar-patch-signals", ev.eventType)
	assert.Contains(t, ev.data, "_customFlag")
	assert.Contains(t, ev.data, "_theme")
}

func TestCtx_doneClosedOnDispose(t *testing.T) {
	t.Parallel()

	doneClosed := make(chan struct{})
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.Init(func(ctx *via.Ctx) {
			go func() {
				<-ctx.Done()
				close(doneClosed)
			}()
		})
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)

	req, _ := http.NewRequest("POST", server.URL+"/_sse/close", bytes.NewBufferString(ctxID))
	resp, err := clientFor(server.URL).Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	select {
	case <-doneClosed:
	case <-time.After(sseTimeout):
		t.Fatal("Done() must close on dispose")
	}
}

func TestCtx_doneNotClosedBeforeDispose(t *testing.T) {
	t.Parallel()

	doneClosed := make(chan struct{})
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.Init(func(ctx *via.Ctx) {
			go func() {
				<-ctx.Done()
				close(doneClosed)
			}()
		})
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)

	select {
	case <-doneClosed:
		t.Fatal("Done() must not close before session ends")
	case <-time.After(50 * time.Millisecond):
	}

	req, err := http.NewRequest("POST", server.URL+"/_sse/close", bytes.NewBufferString(ctxID))
	require.NoError(t, err)
	resp, err := clientFor(server.URL).Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	select {
	case <-doneClosed:
	case <-time.After(sseTimeout):
		t.Fatal("Done() must close when session ends")
	}
}

func TestCtx_exposeWAndRDuringAction(t *testing.T) {
	t.Parallel()

	type result struct{ hasW, hasR bool }
	gotCh := make(chan result, 1)

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		act := cmp.Action(func(ctx *via.Ctx) error {
			gotCh <- result{hasW: ctx.Writer() != nil, hasR: ctx.Request() != nil}
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(act.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	select {
	case r := <-gotCh:
		assert.True(t, r.hasW, "ctx.W should be set during action")
		assert.True(t, r.hasR, "ctx.R should be set during action")
	case <-time.After(sseTimeout):
		require.Fail(t, "timed out waiting for action")
	}
}

func TestCtx_WriterAndRequestAreNilAfterActionReturns(t *testing.T) {
	t.Parallel()

	type result struct{ hasW, hasR bool }
	gotCh := make(chan result, 1)

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		act := cmp.Action(func(ctx *via.Ctx) error {
			go func() {
				time.Sleep(50 * time.Millisecond)
				gotCh <- result{hasW: ctx.Writer() != nil, hasR: ctx.Request() != nil}
			}()
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(act.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	select {
	case r := <-gotCh:
		assert.False(t, r.hasW, "Writer() must return nil from a goroutine that outlives the action")
		assert.False(t, r.hasR, "Request() must return nil from a goroutine that outlives the action")
	case <-time.After(sseTimeout):
		require.Fail(t, "timed out waiting for late goroutine")
	}
}

func TestCtx_WAndRAreNilDuringInit(t *testing.T) {
	t.Parallel()

	type result struct{ hasW, hasR bool }
	var got result

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.Init(func(ctx *via.Ctx) {
			got = result{hasW: ctx.Writer() != nil, hasR: ctx.Request() != nil}
		})
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})

	getPageBody(t, server, "/")

	assert.False(t, got.hasW, "ctx.W should be nil during Init")
	assert.False(t, got.hasR, "ctx.R should be nil during Init")
}

func TestCtx_actionsSerializedPerCtx(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var concurrent int
	var maxConcurrent int
	enterCh := make(chan struct{}, 3)
	proceedCh := make(chan struct{})

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		act := cmp.Action(func(ctx *via.Ctx) error {
			mu.Lock()
			concurrent++
			if concurrent > maxConcurrent {
				maxConcurrent = concurrent
			}
			mu.Unlock()

			enterCh <- struct{}{}
			<-proceedCh

			mu.Lock()
			concurrent--
			mu.Unlock()
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(act.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			triggerAction(t, server.URL, ctxID, actionID)
		}()
	}

	select {
	case <-enterCh:
	case <-time.After(sseTimeout):
		require.Fail(t, "no action entered")
	}
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	max := maxConcurrent
	mu.Unlock()

	close(proceedCh)
	wg.Wait()

	assert.Equal(t, 1, max, "actions should be serialized (max 1 concurrent)")
}

func TestCtx_WAndRClearedAfterActionPanics(t *testing.T) {
	t.Parallel()

	type result struct{ hasW, hasR bool }
	gotCh := make(chan result, 1)

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		panicAct := cmp.Action(func(ctx *via.Ctx) error {
			panic("boom")
		})
		checkAct := cmp.Action(func(ctx *via.Ctx) error {
			gotCh <- result{hasW: ctx.Writer() != nil, hasR: ctx.Request() != nil}
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(panicAct.OnClick(), checkAct.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionIDs := extractActionIDs(t, body)
	require.Len(t, actionIDs, 2)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	// Fire the panicking action
	triggerAction(t, server.URL, ctxID, actionIDs[0])
	time.Sleep(50 * time.Millisecond)

	// Fire the check action — W/R must be freshly set, not stale
	triggerAction(t, server.URL, ctxID, actionIDs[1])

	select {
	case r := <-gotCh:
		assert.True(t, r.hasW, "ctx.W should be set for action after panic")
		assert.True(t, r.hasR, "ctx.R should be set for action after panic")
	case <-time.After(sseTimeout):
		require.Fail(t, "check action never ran — mutex likely stuck after panic")
	}
}

func TestCtx_redirectSendsScriptPatchDuringAction(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		act := cmp.Action(func(ctx *via.Ctx) error {
			ctx.Redirect("/dashboard")
			return nil
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
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "/dashboard")
	assert.Contains(t, ev.data, "<script")
}

func TestCtx_WAndRAreNilAfterActionCompletes(t *testing.T) {
	t.Parallel()

	type result struct{ hasW, hasR bool }
	gotCh := make(chan result, 1)
	actionDone := make(chan struct{})

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		act := cmp.Action(func(ctx *via.Ctx) error {
			close(actionDone)
			return nil
		})
		act2 := cmp.Action(func(ctx *via.Ctx) error {
			gotCh <- result{hasW: ctx.Writer() != nil, hasR: ctx.Request() != nil}
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(act.OnClick(), act2.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionIDs := extractActionIDs(t, body)
	require.Len(t, actionIDs, 2)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	// Trigger first action and wait for it to complete
	triggerAction(t, server.URL, ctxID, actionIDs[0])
	select {
	case <-actionDone:
	case <-time.After(sseTimeout):
		require.Fail(t, "first action never completed")
	}

	// Trigger second action — W/R must be set fresh (not leftover nil from first)
	triggerAction(t, server.URL, ctxID, actionIDs[1])
	select {
	case r := <-gotCh:
		assert.True(t, r.hasW, "ctx.W should be set for each action invocation")
		assert.True(t, r.hasR, "ctx.R should be set for each action invocation")
	case <-time.After(sseTimeout):
		require.Fail(t, "second action never ran")
	}
}

func TestCtx_signalSetValueInViewAppearsInInitialHTML(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		s := via.Signal(cmp, "default_val")
		cmp.View(func(ctx *via.Ctx) h.H {
			s.SetValue(ctx, "overridden_val")
			return h.Div(h.Text("page"))
		})
	})

	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "overridden_val", "signal set during view should appear in initial HTML")
	assert.NotContains(t, body, "default_val", "compile-time default should be replaced")
}

func TestAppSignal_appearsInInitialHTML(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	s := via.AppSignal(app, "_customSig", "default_val")
	app.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("val=%s", s.Get(ctx)))
		})
	})
	t.Cleanup(server.Close)

	body := getPageBody(t, server, "/")
	assert.Contains(t, body, `"_customSig":"default_val"`)
}

func TestAppSignal_setValueInInitAppearsInHTML(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	s := via.AppSignal(app, "_mode", "default")
	app.Page("/", func(cmp *via.Cmp) {
		cmp.Init(func(ctx *via.Ctx) {
			s.SetValue(ctx, "custom")
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("mode=%s", s.Get(ctx)))
		})
	})
	t.Cleanup(server.Close)

	body := getPageBody(t, server, "/")
	assert.Contains(t, body, `"_mode":"custom"`)
	assert.Contains(t, body, "mode=custom")
}

func TestInit_runsOnPageLoad(t *testing.T) {
	t.Parallel()

	initRan := make(chan struct{}, 1)
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.Init(func(ctx *via.Ctx) {
			select {
			case initRan <- struct{}{}:
			default:
			}
		})
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("page")) })
	})

	getPageBody(t, server, "/")

	select {
	case <-initRan:
	case <-time.After(sseTimeout):
		t.Fatal("init must run on page load")
	}
}

func TestInit_runsBeforeView(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	s := via.AppSignal(app, "_pref", "default")
	app.Page("/", func(cmp *via.Cmp) {
		cmp.Init(func(ctx *via.Ctx) {
			s.SetValue(ctx, "from_init")
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("pref=%s", s.Get(ctx)))
		})
	})
	t.Cleanup(server.Close)

	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "pref=from_init", "init must run before view")
}
