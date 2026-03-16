package via_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

func TestState_dirtyTrackingIsActionScoped(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(c *via.Context) {
		s := via.State(c, 0)

		modifyAction := c.Action(func() error {
			s.Set(c, 10)
			return nil
		})

		readAction := c.Action(func() error {
			_ = s.Get(c)
			return nil
		})

		c.View(func() h.H {
			return h.Div(
				h.Textf("count=%d", s.Get(c)),
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
	readSSEEvent(t, stream, sseTimeout)

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionIDs[1])

	_, ev := collectEventOrTimeout(t, stream, 50*time.Millisecond)
	assert.Equal(t, "", ev.eventType, "read-only action should not trigger sync")
}

func TestState_batchMutationsProduceSingleRender(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(c *via.Context) {
		s := via.State(c, 0)

		act := c.Action(func() error {
			s.Set(c, 1)
			s.Set(c, 2)
			s.Set(c, 3)
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

	server := newTestApp(t, "/", func(c *via.Context) {
		stateA := via.State(c, "a")
		stateB := via.State(c, "b")

		act := c.Action(func() error {
			stateA.Set(c, "modified")
			return nil
		})

		c.View(func() h.H {
			return h.Div(
				h.Textf("a=%s", stateA.Get(c)),
				h.Textf("b=%s", stateB.Get(c)),
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

	assert.True(t, strings.Contains(ev.data, "a=modified"))
}

func TestState_unmodifiedStateDoesNotTriggerRender(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(c *via.Context) {
		state := via.State(c, 0)

		act := c.Action(func() error {
			return nil
		})

		c.View(func() h.H {
			return h.Div(
				h.Textf("count=%d", state.Get(c)),
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

	gotEvent, _ := collectEventOrTimeout(t, stream, 50*time.Millisecond)
	assert.False(t, gotEvent, "no patch should be sent when state not modified")
}

func TestState_dirtyFlagClearedAfterSync(t *testing.T) {
	t.Parallel()

	server := newTestApp(t, "/", func(c *via.Context) {
		s := via.State(c, 0)

		act1 := c.Action(func() error {
			s.Set(c, 1)
			c.Sync()
			return nil
		})

		act2 := c.Action(func() error {
			return nil
		})

		c.View(func() h.H {
			return h.Div(
				h.Textf("val=%d", s.Get(c)),
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
	readSSEEvent(t, stream, sseTimeout)

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID1)
	ev := readSSEEvent(t, stream, sseTimeout)
	assert.Equal(t, "datastar-patch-elements", ev.eventType)
	assert.True(t, strings.Contains(ev.data, "val=1"))
}

func TestState_getReturnsInitialValue(t *testing.T) {
	v := via.New()
	var got int
	v.Page("/", func(c *via.Context) {
		s := via.State(c, 0)
		got = s.Get(c)
		c.View(func() h.H { return h.Div() })
	})
	assert.Equal(t, 0, got)
}

func TestState_setUpdatesGet(t *testing.T) {
	v := via.New()
	var got int
	v.Page("/", func(c *via.Context) {
		s := via.State(c, 0)
		s.Set(c, 5)
		got = s.Get(c)
		c.View(func() h.H { return h.Div() })
	})
	assert.Equal(t, 5, got)
}

func TestState_dirtyAfterSet(t *testing.T) {
	v := via.New()
	v.Page("/", func(c *via.Context) {
		s := via.State(c, 0)
		assert.False(t, s.Dirty())
		s.Set(c, 1)
		assert.True(t, s.Dirty())
		c.View(func() h.H { return h.Div() })
	})
}

func TestState_appScopeSharedAcrossContexts(t *testing.T) {
	v := via.New()
	v.Page("/", func(c *via.Context) {
		s := via.State(c, 0, via.WithScopeApp())
		act := c.Action(func() error {
			s.Set(c, s.Get(c)+1)
			c.Sync()
			return nil
		})
		c.View(func() h.H {
			return h.Div(h.Textf("n=%d", s.Get(c)), act.OnClick())
		})
	})
	server := startServer(t, v)

	// First visit: trigger increment action.
	body1 := getPageBody(t, server, "/")
	ctxID1 := extractCtxID(t, body1)
	actionID1 := extractActionID(t, body1)

	stream1, cancel1 := connectSSE(t, server, ctxID1)
	defer cancel1()
	readSSEEvent(t, stream1, sseTimeout)
	time.Sleep(20 * time.Millisecond)
	triggerAction(t, server.URL, ctxID1, actionID1)
	readSSEEvent(t, stream1, sseTimeout) // consume update

	// Second visit: different context, should see incremented value.
	body2 := getPageBody(t, server, "/")
	assert.Contains(t, body2, "n=1")
}

func TestState_appScopeMutexProtected(t *testing.T) {
	v := via.New()
	v.Page("/", func(c *via.Context) {
		s := via.State(c, 0, via.WithScopeApp())
		c.Action(func() error { s.Set(c, s.Get(c)+1); return nil })
		c.View(func() h.H { return h.Div() })
	})
	server := startServer(t, v)

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

func TestState_sessionScopePanics(t *testing.T) {
	v := via.New()
	assert.Panics(t, func() {
		v.Page("/", func(c *via.Context) {
			via.State(c, 0, via.WithScopeSession())
			c.View(func() h.H { return h.Div() })
		})
	})
}

func TestState_conflictingScopesPanics(t *testing.T) {
	v := via.New()
	assert.Panics(t, func() {
		v.Page("/", func(c *via.Context) {
			via.State(c, 0, via.WithScopeApp(), via.WithScopeSession())
			c.View(func() h.H { return h.Div() })
		})
	})
}

func TestState_conflictingAppSessionPanics(t *testing.T) {
	v := via.New()
	assert.Panics(t, func() {
		v.Page("/", func(c *via.Context) {
			via.State(c, 0, via.WithScopeSession(), via.WithScopeApp())
			c.View(func() h.H { return h.Div() })
		})
	})
}
