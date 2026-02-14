package via

import (
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

// TestAction_TypeSafe demonstrates type-safe action creation
func TestAction_TypeSafe(t *testing.T) {
	v := New()

	v.Page("/", func(c *Composition) {
		// Clean API - no method receiver
		action := Action(c, func(ctx *Context) {})

		assert.NotNil(t, action)
		assert.NotEmpty(t, action.ID())

		c.View(func(ctx *Context) h.H {
			return h.Div()
		})
	})
}

// TestAction_ReturnsHandle verifies action registration
func TestAction_ReturnsHandle(t *testing.T) {
	c := &Composition{actions: make(map[string]func(*Context))}
	var executed bool

	action := Action(c, func(ctx *Context) {
		executed = true
	})

	assert.NotNil(t, action)
	assert.NotEmpty(t, action.ID())

	// Verify action was registered
	actionFn, exists := c.actions[action.ID()]
	assert.True(t, exists)
	assert.NotNil(t, actionFn)

	// Execute action
	ctx := NewContext(nil)
	actionFn(ctx)
	assert.True(t, executed)
}

// TestAction_OnClickHelper verifies event binding
func TestAction_OnClickHelper(t *testing.T) {
	c := &Composition{actions: make(map[string]func(*Context))}
	action := Action(c, func(ctx *Context) {})

	onClick := action.OnClick()
	rendered := renderToString(onClick)

	assert.Contains(t, rendered, `data-on:click`)
	assert.Contains(t, rendered, `/_action/`)
	assert.Contains(t, rendered, action.ID())
}

// TestAction_OnChangeHelper verifies input change binding
func TestAction_OnChangeHelper(t *testing.T) {
	c := &Composition{actions: make(map[string]func(*Context))}
	action := Action(c, func(ctx *Context) {})

	onChange := action.OnChange()
	rendered := renderToString(onChange)

	assert.Contains(t, rendered, `data-on:change__debounce.200ms`)
	assert.Contains(t, rendered, `/_action/`)
}

// TestAction_OnKeyDownHelper verifies keyboard binding
func TestAction_OnKeyDownHelper(t *testing.T) {
	c := &Composition{actions: make(map[string]func(*Context))}
	action := Action(c, func(ctx *Context) {})

	onEnter := action.OnKeyDown("Enter")
	rendered := renderToString(onEnter)

	assert.Contains(t, rendered, `data-on:keydown`)
	// HTML-escaped single quotes
	assert.Contains(t, rendered, `evt.key===`)
	assert.Contains(t, rendered, `Enter`)
	assert.Contains(t, rendered, `/_action/`)
}

// TestAction_OnInitHelper verifies page load binding
func TestAction_OnInitHelper(t *testing.T) {
	c := &Composition{actions: make(map[string]func(*Context))}
	action := Action(c, func(ctx *Context) {})

	onInit := action.OnInit()
	rendered := renderToString(onInit)

	assert.Contains(t, rendered, `data-init`)
	assert.Contains(t, rendered, `/_action/`)
}
