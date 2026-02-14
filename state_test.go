package via

import (
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

func TestState_SetGetContract(t *testing.T) {
	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Context)),
	}

	count := State(c, 0)
	ctx := NewContext(nil)

	// Get returns initial value before any Set
	assert.Equal(t, 0, count.Get(ctx))

	// Set updates the value
	count.Set(ctx, 42)
	assert.Equal(t, 42, count.Get(ctx))

	// Set again overwrites
	count.Set(ctx, -1)
	assert.Equal(t, -1, count.Get(ctx))
}

func TestState_GetWithNilSession(t *testing.T) {
	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Context)),
	}

	count := State(c, 7)

	assert.Equal(t, 7, count.Get(nil))

	ctx := &Context{s: nil, mode: sessionModeAction, warn: func(string, ...any) {}}
	assert.Equal(t, 7, count.Get(ctx))
}

// TestState_RegistersWithComposition verifies state registers with composition
func TestState_RegistersWithComposition(t *testing.T) {
	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Context)),
		signals: []signalRegistration{},
		states:  []stateRegistration{},
	}

	count := State(c, 42)

	assert.Len(t, c.states, 1)
	assert.Equal(t, count.id, c.states[0].id)
	assert.Equal(t, 42, c.states[0].initial)
}

func TestScope_TypeExists(t *testing.T) {
	assert.Equal(t, Scope(0), ScopeTab)
	assert.Equal(t, Scope(1), ScopeSession)
	assert.Equal(t, Scope(2), ScopeApp)
}

func TestWithScope_Option(t *testing.T) {
	opt := WithScope(ScopeApp)
	opts := &stateOpts{}
	opt.apply(opts)
	assert.Equal(t, ScopeApp, opts.scope)
}

func TestState_WithScopeApp(t *testing.T) {
	v := New()
	v.Page("/test", func(c *Composition) {
		config := State(c, "default", WithScope(ScopeApp))
		assert.Equal(t, ScopeApp, config.scope)
		c.View(func(ctx *Context) h.H { return h.Div() })
	})
}

func TestState_WithScopeSession(t *testing.T) {
	v := New()
	v.Page("/test", func(c *Composition) {
		token := State(c, "", WithScope(ScopeSession))
		assert.Equal(t, ScopeSession, token.scope)
		c.View(func(ctx *Context) h.H { return h.Div() })
	})
}

func TestState_WithScopeTab_Default(t *testing.T) {
	v := New()
	v.Page("/test", func(c *Composition) {
		count := State(c, 0)
		assert.Equal(t, ScopeTab, count.scope)
		c.View(func(ctx *Context) h.H { return h.Div() })
	})
}

// TestState_AppScope_GetSet tests app-scoped state
func TestState_AppScope_GetSet(t *testing.T) {
	v := New()

	var appState *StateHandle[string]
	v.Page("/test", func(c *Composition) {
		appState = State(c, "initial", WithScope(ScopeApp))
		c.View(func(ctx *Context) h.H { return h.Div() })
	})

	s1 := NewContext(v)
	s2 := NewContext(v)

	// Both sessions see initial value
	assert.Equal(t, "initial", appState.Get(s1))
	assert.Equal(t, "initial", appState.Get(s2))

	// Set from s1
	appState.Set(s1, "updated")

	// Both sessions see updated value (app-scoped)
	assert.Equal(t, "updated", appState.Get(s1))
	assert.Equal(t, "updated", appState.Get(s2))
}
