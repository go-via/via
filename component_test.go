package via

import (
	"strings"
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

// TestComponent_ReturnsHandle verifies that c.Component() returns a non-nil handle
func TestComponent_ReturnsHandle(t *testing.T) {
	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Session)),
		signals: []signalRegistration{},
	}

	composeFn := func(child *Composition) {
		child.View(func(s *Session) h.H {
			return h.Div(h.Text("test"))
		})
	}

	handle := c.Component(composeFn)
	assert.NotNil(t, handle)
}

// TestComponent_MountRendersHTML verifies that .Mount(s) produces expected HTML
func TestComponent_MountRendersHTML(t *testing.T) {
	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Session)),
		signals: []signalRegistration{},
	}

	composeFn := func(child *Composition) {
		child.View(func(s *Session) h.H {
			return h.Div(h.Text("component content"))
		})
	}

	handle := c.Component(composeFn)
	s := NewSession(nil)

	result := handle.Mount(s)
	rendered := renderToString(result)

	assert.Contains(t, rendered, "component content")
}

// TestComponent_MountWrapsInDiv verifies output is wrapped in div with ID, not main
func TestComponent_MountWrapsInDiv(t *testing.T) {
	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Session)),
		signals: []signalRegistration{},
	}

	composeFn := func(child *Composition) {
		child.View(func(s *Session) h.H {
			return h.Div(h.Text("test"))
		})
	}

	handle := c.Component(composeFn)
	s := NewSession(nil)

	result := handle.Mount(s)
	rendered := renderToString(result)

	assert.Contains(t, rendered, `<div id="`)
	assert.Contains(t, rendered, handle.id)
	assert.NotContains(t, rendered, "<main")
}

// TestComponent_HasUniqueID verifies each component gets a unique ID
func TestComponent_HasUniqueID(t *testing.T) {
	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Session)),
		signals: []signalRegistration{},
	}

	composeFn := func(child *Composition) {
		child.View(func(s *Session) h.H {
			return h.Div(h.Text("test"))
		})
	}

	handle1 := c.Component(composeFn)
	handle2 := c.Component(composeFn)

	assert.NotEqual(t, handle1.id, handle2.id)
}

// TestComponent_WithState verifies state inside component works via shared Session
func TestComponent_WithState(t *testing.T) {
	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Session)),
		signals: []signalRegistration{},
	}

	var stateHandle *StateHandle[int]

	composeFn := func(child *Composition) {
		stateHandle = State(child, 42)
		child.View(func(s *Session) h.H {
			return h.Div(h.Textf("Count: %d", stateHandle.Get(s)))
		})
	}

	handle := c.Component(composeFn)
	s := NewSession(nil)

	// Verify initial state
	result := handle.Mount(s)
	rendered := renderToString(result)
	assert.Contains(t, rendered, "Count: 42")

	// Update state
	stateHandle.Set(s, 99)
	result = handle.Mount(s)
	rendered = renderToString(result)
	assert.Contains(t, rendered, "Count: 99")
}

// TestComponent_WithAction verifies action registered on parent, fires correctly
func TestComponent_WithAction(t *testing.T) {
	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Session)),
		signals: []signalRegistration{},
	}

	var executed bool

	composeFn := func(child *Composition) {
		action := Action(child, func(s *Session) {
			executed = true
		})
		child.View(func(s *Session) h.H {
			return h.Button(h.Text("Click"), action.OnClick())
		})
	}

	_ = c.Component(composeFn)
	s := NewSession(nil)

	// Verify action registered on parent
	assert.NotEmpty(t, c.actions)

	// Find and execute the action
	for _, actionFn := range c.actions {
		actionFn(s)
	}

	assert.True(t, executed)
}

// TestComponent_WithSignal verifies signal registered on parent, included in page
func TestComponent_WithSignal(t *testing.T) {
	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Session)),
		signals: []signalRegistration{},
	}

	composeFn := func(child *Composition) {
		count := Signal(child, 10)
		child.View(func(s *Session) h.H {
			return h.Div(count.Text())
		})
	}

	c.Component(composeFn)

	// Verify signal registered on parent
	assert.Len(t, c.signals, 1)
	assert.Equal(t, 10, c.signals[0].initial)
}

// TestComponent_Nesting verifies parent component embeds child via Mount(), both render
func TestComponent_Nesting(t *testing.T) {
	parent := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Session)),
		signals: []signalRegistration{},
	}

	childCompose := func(child *Composition) {
		child.View(func(s *Session) h.H {
			return h.Div(h.Text("child content"))
		})
	}

	parentCompose := func(p *Composition) {
		childHandle := p.Component(childCompose)
		p.View(func(s *Session) h.H {
			return h.Div(
				h.Text("parent content"),
				childHandle.Mount(s),
			)
		})
	}

	handle := parent.Component(parentCompose)
	s := NewSession(nil)

	result := handle.Mount(s)
	rendered := renderToString(result)

	assert.Contains(t, rendered, "parent content")
	assert.Contains(t, rendered, "child content")
}

// TestComponent_ActionSyncsFragment verifies component action sends only component HTML, not full page
func TestComponent_ActionSyncsFragment(t *testing.T) {
	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Session)),
		signals: []signalRegistration{},
		states:  []stateRegistration{},
	}

	var compAction *ActionHandle
	var countState *StateHandle[int]

	composeFn := func(child *Composition) {
		countState = State(child, 0)
		compAction = Action(child, func(s *Session) {
			countState.Set(s, 10)
		})
		child.View(func(s *Session) h.H {
			return h.P(h.Textf("Count: %d", countState.Get(s)))
		})
	}

	handle := c.Component(composeFn)

	c.View(func(s *Session) h.H {
		return h.Div(
			h.H1(h.Text("Page Title")),
			handle.Mount(s),
		)
	})

	// Create session with patchChan
	sess := &session{
		id:        c.id,
		store:     newStore(),
		patchChan: make(chan patch, 10),
		c:         c,
	}

	sc := &Session{
		s:    sess.store,
		ss:   sess,
		mode: sessionModeAction,
		warn: func(string, ...any) {},
	}

	// Set component context like actionHTTPHandler does
	if owner, ok := c.actionOwners[compAction.ID()]; ok {
		sc.compID = owner.id
		sc.compViewFn = owner.viewFn
	}

	// Execute the component action
	actionFn := c.actions[compAction.ID()]
	actionFn(sc)

	// Read the patch
	select {
	case p := <-sess.patchChan:
		content := p.content
		// Should contain component content
		assert.Contains(t, content, "Count: 10")
		// Should contain component div ID
		assert.Contains(t, content, handle.id)
		// Should NOT contain page-level content
		assert.NotContains(t, content, "Page Title")
		// Should NOT contain <main
		assert.NotContains(t, content, "<main")
	default:
		t.Fatal("Expected fragment patch from component action")
	}
}

// TestComponent_ActionSyncsFragment_NestedComponent verifies nested component actions sync only innermost fragment
func TestComponent_ActionSyncsFragment_NestedComponent(t *testing.T) {
	root := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Session)),
		signals: []signalRegistration{},
		states:  []stateRegistration{},
	}

	var innerAction *ActionHandle
	var innerHandle *CompHandle

	innerCompose := func(child *Composition) {
		count := State(child, 0)
		innerAction = Action(child, func(s *Session) {
			count.Set(s, 5)
		})
		child.View(func(s *Session) h.H {
			return h.P(h.Textf("Inner: %d", count.Get(s)))
		})
	}

	outerCompose := func(child *Composition) {
		innerHandle = child.Component(innerCompose)
		child.View(func(s *Session) h.H {
			return h.Div(
				h.H2(h.Text("Outer")),
				innerHandle.Mount(s),
			)
		})
	}

	outerHandle := root.Component(outerCompose)

	root.View(func(s *Session) h.H {
		return h.Div(
			h.H1(h.Text("Root")),
			outerHandle.Mount(s),
		)
	})

	sess := &session{
		id:        root.id,
		store:     newStore(),
		patchChan: make(chan patch, 10),
		c:         root,
	}

	sc := &Session{
		s:    sess.store,
		ss:   sess,
		mode: sessionModeAction,
		warn: func(string, ...any) {},
	}

	// Set component context like actionHTTPHandler does
	if owner, ok := root.actionOwners[innerAction.ID()]; ok {
		sc.compID = owner.id
		sc.compViewFn = owner.viewFn
	}

	actionFn := root.actions[innerAction.ID()]
	actionFn(sc)

	select {
	case p := <-sess.patchChan:
		content := p.content
		assert.Contains(t, content, "Inner: 5")
		assert.Contains(t, content, innerHandle.id)
		assert.NotContains(t, content, "Root")
		assert.NotContains(t, content, "<main")
	default:
		t.Fatal("Expected fragment patch from nested component action")
	}
}

// TestComponent_PageActionStillSyncsFullPage verifies page-level actions still sync full page
func TestComponent_PageActionStillSyncsFullPage(t *testing.T) {
	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Session)),
		signals: []signalRegistration{},
		states:  []stateRegistration{},
	}

	var pageAction *ActionHandle
	var pageState *StateHandle[string]

	composeFn := func(child *Composition) {
		child.View(func(s *Session) h.H {
			return h.P(h.Text("component"))
		})
	}

	handle := c.Component(composeFn)

	pageState = State(c, "hello")
	pageAction = Action(c, func(s *Session) {
		pageState.Set(s, "world")
	})

	c.View(func(s *Session) h.H {
		return h.Div(
			h.H1(h.Textf("Page: %s", pageState.Get(s))),
			handle.Mount(s),
		)
	})

	sess := &session{
		id:        c.id,
		store:     newStore(),
		patchChan: make(chan patch, 10),
		c:         c,
	}

	sc := &Session{
		s:    sess.store,
		ss:   sess,
		mode: sessionModeAction,
		warn: func(string, ...any) {},
	}

	actionFn := c.actions[pageAction.ID()]
	actionFn(sc)

	select {
	case p := <-sess.patchChan:
		content := p.content
		// Page action should render full page view (wrapped in <main>)
		assert.Contains(t, content, "Page: world")
		assert.Contains(t, content, "<main")
	default:
		t.Fatal("Expected full page patch from page action")
	}
}

// TestComponent_SyncFragment_ManualCall verifies SyncFragment sends arbitrary HTML as fragment
func TestComponent_SyncFragment_ManualCall(t *testing.T) {
	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Session)),
		signals: []signalRegistration{},
		states:  []stateRegistration{},
	}
	c.View(func(s *Session) h.H {
		return h.Div(h.Text("page"))
	})

	sess := &session{
		id:        c.id,
		store:     newStore(),
		patchChan: make(chan patch, 10),
		c:         c,
	}

	sc := &Session{
		s:    sess.store,
		ss:   sess,
		mode: sessionModeAction,
		warn: func(string, ...any) {},
	}

	fragment := h.Div(h.ID("my-fragment"), h.Text("partial update"))
	sc.SyncFragment(fragment)

	select {
	case p := <-sess.patchChan:
		content := p.content
		assert.Contains(t, content, "partial update")
		assert.Contains(t, content, "my-fragment")
		// Should NOT contain page content
		assert.NotContains(t, content, "page")
	default:
		t.Fatal("Expected fragment patch from SyncFragment")
	}
}

// TestComponent_SyncFragment_ViewModeWarns verifies SyncFragment warns in view mode
func TestComponent_SyncFragment_ViewModeWarns(t *testing.T) {
	var warned bool
	sc := &Session{
		s:    newStore(),
		mode: sessionModeView,
		warn: func(string, ...any) { warned = true },
	}

	sc.SyncFragment(h.Div(h.Text("nope")))
	assert.True(t, warned)
}

// TestComponent_MultipleInstances verifies two instances with different props have independent state
func TestComponent_MultipleInstances(t *testing.T) {
	c := &Composition{
		id:      genRandID(),
		actions: make(map[string]func(*Session)),
		signals: []signalRegistration{},
	}

	type CounterProps struct {
		Name  string
		Start int
	}

	makeCounter := func(props CounterProps) ComposeFn {
		return func(child *Composition) {
			count := State(child, props.Start)
			child.View(func(s *Session) h.H {
				return h.Div(
					h.Text(props.Name),
					h.Textf(": %d", count.Get(s)),
				)
			})
		}
	}

	counter1 := c.Component(makeCounter(CounterProps{Name: "A", Start: 1}))
	counter2 := c.Component(makeCounter(CounterProps{Name: "B", Start: 100}))

	s := NewSession(nil)

	result1 := renderToString(counter1.Mount(s))
	result2 := renderToString(counter2.Mount(s))

	assert.Contains(t, result1, "A")
	assert.Contains(t, result1, ": 1")
	assert.Contains(t, result2, "B")
	assert.Contains(t, result2, ": 100")

	// Verify they have different IDs
	assert.NotEqual(t, counter1.id, counter2.id)
	assert.True(t, strings.Contains(result1, counter1.id))
	assert.True(t, strings.Contains(result2, counter2.id))
}
