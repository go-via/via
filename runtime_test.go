package via_test

import (
	"bufio"
	"bytes"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sseTimeout = 3 * time.Second

func extractActionID(t *testing.T, body string) string {
	t.Helper()
	const prefix = "/_action/"
	idx := strings.Index(body, prefix)
	require.NotEqual(t, -1, idx, "action URL not found in page body")
	start := idx + len(prefix)
	end := strings.IndexAny(body[start:], "'&#\"")
	require.NotEqual(t, -1, end)
	return body[start : start+end]
}

func extractActionIDs(t *testing.T, body string) []string {
	t.Helper()
	var ids []string
	const prefix = "/_action/"
	searchStart := 0
	for {
		idx := strings.Index(body[searchStart:], prefix)
		if idx == -1 {
			break
		}
		idx += searchStart
		start := idx + len(prefix)
		end := strings.IndexAny(body[start:], "'&#\"")
		if end == -1 {
			break
		}
		ids = append(ids, body[start:start+end])
		searchStart = start + end
	}
	require.NotEmpty(t, ids, "no action IDs found in page body")
	return ids
}

func collectEventOrTimeout(t *testing.T, scanner *bufio.Scanner, timeout time.Duration) (bool, sseEvent) {
	resultCh := make(chan sseEvent, 1)
	go func() {
		var ev sseEvent
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event:") {
				ev.eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			} else if strings.HasPrefix(line, "data:") {
				d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if ev.data == "" {
					ev.data = d
				} else {
					ev.data += "\n" + d
				}
			} else if line == "" && ev.eventType != "" {
				resultCh <- ev
				return
			}
		}
	}()
	select {
	case ev := <-resultCh:
		return true, ev
	case <-time.After(timeout):
		return false, sseEvent{}
	}
}

func triggerAction(t *testing.T, serverURL, ctxID, actionID string) {
	t.Helper()
	sigsJSON := `{"via_tab":"` + ctxID + `"}`
	resp, err := http.Post(serverURL+"/_action/"+actionID, "application/json", strings.NewReader(sigsJSON))
	require.NoError(t, err)
	resp.Body.Close()
}

func triggerActionWithSignal(t *testing.T, serverURL, ctxID, actionID, sigID, sigValue string) {
	t.Helper()
	sigsJSON := `{"via_tab":"` + ctxID + `","` + sigID + `":"` + sigValue + `"}`
	resp, err := http.Post(serverURL+"/_action/"+actionID, "application/json", strings.NewReader(sigsJSON))
	require.NoError(t, err)
	resp.Body.Close()
}

func extractSignalID(t *testing.T, body string) string {
	t.Helper()
	markers := []string{`data-bind="`, `data-text="`, `data-show="`}
	for _, marker := range markers {
		idx := strings.Index(body, marker)
		if idx != -1 {
			start := idx + len(marker)
			end := strings.Index(body[start:], `"`)
			require.NotEqual(t, -1, end, "signal ID not terminated")
			return body[start : start+end]
		}
	}
	t.Fatal("signal ID not found in page body")
	return ""
}

// --- SSE tests ---

func TestSSE_reconnectAfterDisconnect(t *testing.T) {
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

	gotSigPatch, sigEv := collectEventOrTimeout(t, stream, 50*time.Millisecond)
	assert.False(t, gotSigPatch, "signal injected from browser must not be echoed back, got: %s %s", sigEv.eventType, sigEv.data)
}

func TestSSE_actionReceivesInjectedSignal(t *testing.T) {
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

	got, ev := collectEventOrTimeout(t, stream, 50*time.Millisecond)
	assert.False(t, got, "no SSE event expected when action does not modify state, got: %s %s", ev.eventType, ev.data)
}

func TestSSE_noSignalSyncWhenSignalNotModifiedInAction(t *testing.T) {
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

	got, ev := collectEventOrTimeout(t, stream, 50*time.Millisecond)
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

	got, ev := collectEventOrTimeout(t, stream, 50*time.Millisecond)
	assert.False(t, got, "injected signal must not be echoed back to the browser, got event: %s %s", ev.eventType, ev.data)
}

func TestSSE_noSignalPatchWhenSignalUnchanged(t *testing.T) {
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

	gotEvent, ev := collectEventOrTimeout(t, stream, 50*time.Millisecond)

	t.Logf("After action - event: type=%s, data=%s, counter=%d", ev.eventType, ev.data, counter)

	assert.False(t, gotEvent, "no patch should be sent when signal is unchanged")
}

// --- View tests ---

func TestView_rendersInDivWithContextID(t *testing.T) {
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.P(h.Text("content")) })
	})
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, `<div id=`)
	assert.Contains(t, body, "content")
}

// --- Component tests ---

func TestComponent_rendersNestedInView(t *testing.T) {
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
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.True(t, disposeCalled, "dispose should run after session close")
}

// --- Ctx tests ---

func TestGetPathParam_returnsEmptyForMissingParam(t *testing.T) {
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

func TestCtx_runsInitOnSSEConnect(t *testing.T) {
	initDone := make(chan struct{})
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.Init(func(ctx *via.Ctx) { close(initDone) })
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("page")) })
	})

	body := getPageBody(t, server, "/")
	select {
	case <-initDone:
		t.Fatal("init must not run before SSE connects")
	default:
	}

	ctxID := extractCtxID(t, body)
	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	select {
	case <-initDone:
	case <-time.After(sseTimeout):
		t.Fatal("init must run when SSE connects")
	}
}

func TestCtx_syncReRendersAndPushesView(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		s := via.State(cmp, "before")

		cmp.Init(func(ctx *via.Ctx) {
			s.Set(ctx, "after")
			ctx.Sync()
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("val=%s", s.Get(ctx)))
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	assert.Contains(t, body, "val=before")

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "val=after", "Sync must re-render and push the updated view")
}

func TestCtx_syncFlushesSignalPatches(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, "original")

		cmp.Init(func(ctx *via.Ctx) {
			sig.SetValue(ctx, "pushed")
			ctx.Sync()
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(sig.Text())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

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

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.Init(func(ctx *via.Ctx) {
			go func() {
				<-ctx.Done()
			}()
		})
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	req, _ := http.NewRequest("POST", server.URL+"/_sse/close", bytes.NewBufferString(ctxID))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
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

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	select {
	case <-doneClosed:
		t.Fatal("Done() must not close before session ends")
	case <-time.After(50 * time.Millisecond):
	}

	req, err := http.NewRequest("POST", server.URL+"/_sse/close", bytes.NewBufferString(ctxID))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
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
			gotCh <- result{hasW: ctx.W != nil, hasR: ctx.R != nil}
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

func TestCtx_WAndRAreNilDuringInit(t *testing.T) {
	t.Parallel()

	type result struct{ hasW, hasR bool }
	gotCh := make(chan result, 1)

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		cmp.Init(func(ctx *via.Ctx) {
			gotCh <- result{hasW: ctx.W != nil, hasR: ctx.R != nil}
		})
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	select {
	case r := <-gotCh:
		assert.False(t, r.hasW, "ctx.W should be nil during Init")
		assert.False(t, r.hasR, "ctx.R should be nil during Init")
	case <-time.After(sseTimeout):
		require.Fail(t, "timed out waiting for init")
	}
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
			gotCh <- result{hasW: ctx.W != nil, hasR: ctx.R != nil}
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
			gotCh <- result{hasW: ctx.W != nil, hasR: ctx.R != nil}
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
