package via_test

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

// TestState_getReturnsInitialValue verifies Get() returns the value passed at construction.
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

// TestState_setUpdatesGet verifies Set() changes the value returned by Get().
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

// TestState_dirtyAfterSet verifies the dirty flag is set after a Set() call.
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

// TestAction_autoSyncsAfterExecution verifies that actions without explicit c.Sync() still push updates.
func TestAction_autoSyncsAfterExecution(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		s := via.State(c, 0)
		act := c.Action(func() {
			s.Set(c, 42)
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

// TestState_appScopeSharedAcrossContexts verifies two page visits share the same app-scoped state.
func TestState_appScopeSharedAcrossContexts(t *testing.T) {
	v := via.New()
	v.Page("/", func(c *via.Context) {
		s := via.State(c, 0, via.WithScopeApp())
		act := c.Action(func() {
			s.Set(c, s.Get(c)+1)
			c.Sync()
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

// TestState_appScopeMutexProtected verifies concurrent Set/Get on app-scoped state does not race.
func TestState_appScopeMutexProtected(t *testing.T) {
	v := via.New()
	v.Page("/", func(c *via.Context) {
		s := via.State(c, 0, via.WithScopeApp())
		c.Action(func() { s.Set(c, s.Get(c)+1) })
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

// TestState_sessionScopePanics verifies that requesting ScopeSession panics (deferred to design 07).
func TestState_sessionScopePanics(t *testing.T) {
	v := via.New()
	assert.Panics(t, func() {
		v.Page("/", func(c *via.Context) {
			via.State(c, 0, via.WithScopeSession())
			c.View(func() h.H { return h.Div() })
		})
	})
}

// TestState_conflictingScopesPanics verifies that passing multiple scope options panics.
func TestState_conflictingScopesPanics(t *testing.T) {
	v := via.New()
	assert.Panics(t, func() {
		v.Page("/", func(c *via.Context) {
			via.State(c, 0, via.WithScopeApp(), via.WithScopeSession())
			c.View(func() h.H { return h.Div() })
		})
	})
}

// TestState_conflictingAppSessionPanics verifies session+app order also panics.
func TestState_conflictingAppSessionPanics(t *testing.T) {
	v := via.New()
	assert.Panics(t, func() {
		v.Page("/", func(c *via.Context) {
			via.State(c, 0, via.WithScopeSession(), via.WithScopeApp())
			c.View(func() h.H { return h.Div() })
		})
	})
}
