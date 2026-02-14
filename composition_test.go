package via

import (
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

func TestComposition_View_PanicsOnNil(t *testing.T) {
	c := &Composition{
		actions:      make(map[string]func(*Context)),
		actionOwners: make(map[string]compOwner),
	}

	assert.Panics(t, func() {
		c.View(nil)
	}, "View(nil) should panic")
}

func TestComposition_View_WrapsInMain(t *testing.T) {
	c := &Composition{
		id:           "test-id",
		route:        "/test",
		actions:      make(map[string]func(*Context)),
		actionOwners: make(map[string]compOwner),
	}

	c.View(func(ctx *Context) h.H {
		return h.Div(h.Text("content"))
	})

	assert.NotNil(t, c.viewFn)
}

func TestComposition_View_ComponentSkipsMain(t *testing.T) {
	c := &Composition{
		id:           "comp-id",
		route:        "",
		actions:      make(map[string]func(*Context)),
		actionOwners: make(map[string]compOwner),
		isComponent:  true,
	}

	c.View(func(ctx *Context) h.H {
		return h.Div(h.Text("component content"))
	})

	assert.NotNil(t, c.viewFn)
}

func TestComposition_State_AfterViewPanics(t *testing.T) {
	c := &Composition{
		actions:      make(map[string]func(*Context)),
		actionOwners: make(map[string]compOwner),
	}

	c.View(func(ctx *Context) h.H {
		return h.Div(h.Text("test"))
	})

	assert.Panics(t, func() {
		State(c, 42)
	}, "State() after View() should panic")
}

func TestComposition_Signal_AfterViewPanics(t *testing.T) {
	c := &Composition{
		actions:      make(map[string]func(*Context)),
		actionOwners: make(map[string]compOwner),
	}

	c.View(func(ctx *Context) h.H {
		return h.Div(h.Text("test"))
	})

	assert.Panics(t, func() {
		Signal(c, 42)
	}, "Signal() after View() should panic")
}

func TestComposition_State_BeforeViewOK(t *testing.T) {
	c := &Composition{
		actions:      make(map[string]func(*Context)),
		actionOwners: make(map[string]compOwner),
	}

	state := State(c, 42)

	c.View(func(ctx *Context) h.H {
		return h.Div(h.Textf("Count: %d", state.Get(ctx)))
	})

	assert.NotNil(t, state)
}

func TestComposition_Signal_BeforeViewOK(t *testing.T) {
	c := &Composition{
		actions:      make(map[string]func(*Context)),
		actionOwners: make(map[string]compOwner),
	}

	signal := Signal(c, "hello")

	c.View(func(ctx *Context) h.H {
		return h.Div(h.Text(signal.Get(ctx)))
	})

	assert.NotNil(t, signal)
}

func TestComposition_Action_AfterViewOK(t *testing.T) {
	c := &Composition{
		actions:      make(map[string]func(*Context)),
		actionOwners: make(map[string]compOwner),
	}

	c.View(func(ctx *Context) h.H {
		return h.Div(h.Text("test"))
	})

	action := Action(c, func(ctx *Context) {})

	assert.NotNil(t, action)
}

func TestComposition_ID(t *testing.T) {
	c := &Composition{
		id: "test-id",
	}

	assert.Equal(t, "test-id", c.ID())
}

func TestComposition_TestActions(t *testing.T) {
	c := &Composition{
		actions: make(map[string]func(*Context)),
	}

	actionFn := func(ctx *Context) {}
	c.actions["test-action"] = actionFn

	actions := c.TestActions()
	assert.Contains(t, actions, "test-action")
}

func TestComposition_TestActionOwners(t *testing.T) {
	c := &Composition{
		actionOwners: make(map[string]compOwner),
	}

	owners := c.TestActionOwners()
	assert.NotNil(t, owners)
}
