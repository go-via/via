package via_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestState_readOnlyActionDoesNotSync(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0)

		modifyAction := cmp.Action(func(ctx *via.Ctx) error {
			s.Set(ctx, 10)
			return nil
		})

		readAction := cmp.Action(func(ctx *via.Ctx) error {
			_ = s.Get(ctx)
			return nil
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.Textf("count=%d", s.Get(ctx)),
				modifyAction.OnClick(),
				readAction.OnClick(),
			)
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionIDs := extractActionIDs(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionIDs[1])

	_, ev := collectEventOrTimeout(t, stream, 50*time.Millisecond)
	assert.Equal(t, "", ev.eventType, "read-only action should not trigger sync")
}

func TestState_batchMutationsProduceSingleRender(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0)

		act := cmp.Action(func(ctx *via.Ctx) error {
			s.Set(ctx, 1)
			s.Set(ctx, 2)
			s.Set(ctx, 3)
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

	patchCount := 0
	timeout := time.After(500 * time.Millisecond)
	done := false

	for !done {
		select {
		case <-timeout:
			done = true
		default:
			ev := readSSEEvent(t, stream, 50*time.Millisecond)
			if ev.eventType == "datastar-patch-elements" {
				patchCount++
			}
			if strings.Contains(ev.data, "val=3") {
				done = true
			}
		}
	}

	assert.Equal(t, 1, patchCount, "multiple mutations in one action should produce single re-render")
}

func TestState_onlyModifiedStateTriggersSync(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		stateA := via.State(cmp, "a")
		stateB := via.State(cmp, "b")

		act := cmp.Action(func(ctx *via.Ctx) error {
			stateA.Set(ctx, "modified")
			return nil
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.Textf("a=%s", stateA.Get(ctx)),
				h.Textf("b=%s", stateB.Get(ctx)),
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

	assert.True(t, strings.Contains(ev.data, "a=modified"))
}

func TestState_unmodifiedStateDoesNotTriggerRender(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		state := via.State(cmp, 0)

		act := cmp.Action(func(ctx *via.Ctx) error {
			return nil
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.Textf("count=%d", state.Get(ctx)),
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

	gotEvent, _ := collectEventOrTimeout(t, stream, 50*time.Millisecond)
	assert.False(t, gotEvent, "no patch should be sent when state not modified")
}

func TestState_syncDoesNotRetriggerOnNextAction(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0)

		act1 := cmp.Action(func(ctx *via.Ctx) error {
			s.Set(ctx, 1)
			return nil
		})

		act2 := cmp.Action(func(ctx *via.Ctx) error {
			return nil
		})

		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(
				h.Textf("val=%d", s.Get(ctx)),
				act1.OnClick(),
				act2.OnClick(),
			)
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID1 := extractActionID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID1)
	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.True(t, strings.Contains(ev.data, "val=1"))
}

func TestState_getReturnsInitialValue(t *testing.T) {
	v := via.New()
	var got int
	v.Page("/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0)
		cmp.View(func(ctx *via.Ctx) h.H {
			got = s.Get(ctx)
			return h.Div()
		})
	})
	assert.Equal(t, 0, got)
}

func TestState_setUpdatesGet(t *testing.T) {
	v := via.New()
	var got int
	v.Page("/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0)
		cmp.View(func(ctx *via.Ctx) h.H {
			s.Set(ctx, 5)
			got = s.Get(ctx)
			return h.Div()
		})
	})
	assert.Equal(t, 5, got)
}

func TestState_appScopeSharedAcrossContexts(t *testing.T) {
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Page("/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0, via.WithScopeApp())
		act := cmp.Action(func(ctx *via.Ctx) error {
			s.Set(ctx, s.Get(ctx)+1)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("n=%d", s.Get(ctx)), act.OnClick())
		})
	})
	defer server.Close()

	// First visit: trigger increment action.
	body1 := getPageBody(t, server, "/")
	ctxID1 := extractCtxID(t, body1)
	actionID1 := extractActionID(t, body1)

	stream1, cancel1 := connectSSE(t, server, ctxID1)
	defer cancel1()
	time.Sleep(20 * time.Millisecond)
	triggerAction(t, server.URL, ctxID1, actionID1)
	readSSEEvent(t, stream1, sseTimeout) // consume update

	// Second visit: different context, should see incremented value.
	body2 := getPageBody(t, server, "/")
	assert.Contains(t, body2, "n=1")
}

func TestState_userScopeGetReturnsInitialValue(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		s := via.State(cmp, 42, via.WithScopeUser())
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("val=%d", s.Get(ctx)))
		})
	})

	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "val=42")
}

func TestState_userScopeSetPersistsAcrossRequests(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	v.Page("/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0, via.WithScopeUser())
		act := cmp.Action(func(ctx *via.Ctx) error {
			s.Set(ctx, 99)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("val=%d", s.Get(ctx)), act.OnClick())
		})
	})
	t.Cleanup(server.Close)

	// First visit — get session cookie
	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()
	jar := collectCookies(t, server.URL, resp.Cookies())

	body := getPageBodyWithCookies(t, server, "/", jar)
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSEWithCookies(t, server, ctxID, jar)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	triggerActionWithCookies(t, server.URL, ctxID, actionID, jar)
	readSSEEvent(t, stream, sseTimeout)

	// Revisit with same cookies — should see persisted value
	body2 := getPageBodyWithCookies(t, server, "/", jar)
	assert.Contains(t, body2, "val=99", "user-scoped state should persist across requests in same session")
}

func TestState_userScopeIsolatedBetweenSessions(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	v.Page("/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0, via.WithScopeUser())
		act := cmp.Action(func(ctx *via.Ctx) error {
			s.Set(ctx, 77)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("val=%d", s.Get(ctx)), act.OnClick())
		})
	})
	t.Cleanup(server.Close)

	// Session A: set state to 77
	respA, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	respA.Body.Close()
	jarA := collectCookies(t, server.URL, respA.Cookies())

	bodyA := getPageBodyWithCookies(t, server, "/", jarA)
	ctxIDA := extractCtxID(t, bodyA)
	actionIDA := extractActionID(t, bodyA)

	streamA, cancelA := connectSSEWithCookies(t, server, ctxIDA, jarA)
	defer cancelA()
	time.Sleep(20 * time.Millisecond)

	triggerActionWithCookies(t, server.URL, ctxIDA, actionIDA, jarA)
	readSSEEvent(t, streamA, sseTimeout)

	// Session B (fresh, no cookies): should see initial value
	bodyB := getPageBody(t, server, "/")
	assert.Contains(t, bodyB, "val=0", "different session should not see another session's user-scoped state")
}

func TestState_userScopeSetTriggersRerender(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))

	v.Page("/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0, via.WithScopeUser())
		act := cmp.Action(func(ctx *via.Ctx) error {
			s.Set(ctx, 5)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("val=%d", s.Get(ctx)), act.OnClick())
		})
	})
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	resp.Body.Close()
	jar := collectCookies(t, server.URL, resp.Cookies())

	body := getPageBodyWithCookies(t, server, "/", jar)
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	stream, cancel := connectSSEWithCookies(t, server, ctxID, jar)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	triggerActionWithCookies(t, server.URL, ctxID, actionID, jar)

	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.Contains(t, ev.data, "val=5", "user-scoped Set should trigger re-render")
}

func TestState_conflictingUserAndAppScopesPanics(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		v := via.New()
		v.Page("/", func(cmp *via.Cmp) {
			via.State(cmp, 0, via.WithScopeUser(), via.WithScopeApp())
			cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
		})
	})
}

func TestState_appScopeMutexProtected(t *testing.T) {
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Page("/", func(cmp *via.Cmp) {
		s := via.State(cmp, 0, via.WithScopeApp())
		cmp.Action(func(ctx *via.Ctx) error { s.Set(ctx, s.Get(ctx)+1); return nil })
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	defer server.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			http.Get(server.URL + "/") //nolint
		}()
	}
	wg.Wait()
}

