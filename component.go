package via

import (
	"maps"

	"github.com/go-via/via/h"
)

// ComposeFn is the compose function for a component (same shape as a page's).
type ComposeFn func(c *Composition)

// CompHandle is a handle to a composed component.
type CompHandle struct {
	id     string
	viewFn func(*Context) h.H
}

// Mount renders the component into the parent view, wrapped in a div with its ID.
func (ch *CompHandle) Mount(ctx *Context) h.H {
	return h.Div(h.ID(ch.id), ch.viewFn(ctx))
}

// Component creates a child component from a compose function.
func (parent *Composition) Component(composeFn ComposeFn) *CompHandle {
	// Create child composition with its own ID
	child := &Composition{
		id:          genRandID(),
		actions:     make(map[string]func(*Context)),
		signals:     []signalRegistration{},
		states:      []stateRegistration{},
		isComponent: true,
	}

	// Call composeFn to let component configure itself
	composeFn(child)

	// Merge child's actions into parent, tracking component ownership
	if parent.actionOwners == nil {
		parent.actionOwners = make(map[string]compOwner)
	}
	owner := compOwner{id: child.id, viewFn: child.viewFn}
	for id, fn := range child.actions {
		parent.actions[id] = fn
		parent.actionOwners[id] = owner
	}
	// Propagate any existing action owners from child (for nested components)
	maps.Copy(parent.actionOwners, child.actionOwners)

	// Merge child's signals into parent
	parent.signals = append(parent.signals, child.signals...)

	// Merge child's states into parent
	parent.states = append(parent.states, child.states...)

	// Return handle with unwrapped viewFn
	return &CompHandle{
		id:     child.id,
		viewFn: child.viewFn,
	}
}
