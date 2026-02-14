package via

import (
	"fmt"

	"github.com/go-via/via/h"
)

func Action(c *Composition, fn func(ctx *Context)) *ActionHandle {
	if c.actions == nil {
		c.actions = make(map[string]func(*Context))
	}
	idStr := genRandID()
	c.actions[idStr] = fn
	return &ActionHandle{id: idStr}
}

// ActionHandle represents a handle to an event handler fn
type ActionHandle struct {
	id string
}

// ID returns the action handle's unique identifier.
func (a *ActionHandle) ID() string {
	return a.id
}

// ActionHandleOption configures behavior of action handles
type ActionHandleOption interface {
	apply(*triggerOpts)
}

type triggerOpts struct {
	hasSignal bool
	signalID  string
	value     string
}

type withSignalOpt struct {
	signalID string
	value    string
}

func (o withSignalOpt) apply(opts *triggerOpts) {
	opts.hasSignal = true
	opts.signalID = o.signalID
	opts.value = o.value
}

func buildOnExpr(base string, opts *triggerOpts) string {
	if !opts.hasSignal {
		return base
	}
	return fmt.Sprintf("$%s=%s;%s", opts.signalID, opts.value, base)
}

func applyOptions(options ...ActionHandleOption) triggerOpts {
	var opts triggerOpts
	for _, opt := range options {
		opt.apply(&opts)
	}
	return opts
}

func actionURL(id string) string {
	return fmt.Sprintf("@get('/_action/%s')", id)
}

// OnClick returns a via.h DOM attribute that triggers on click.
func (a *ActionHandle) OnClick(options ...ActionHandleOption) h.H {
	opts := applyOptions(options...)
	return h.Data("on:click", buildOnExpr(actionURL(a.id), &opts))
}

// OnChange returns a via.h DOM attribute that triggers on input change.
func (a *ActionHandle) OnChange(options ...ActionHandleOption) h.H {
	opts := applyOptions(options...)
	return h.Data("on:change__debounce.200ms", buildOnExpr(actionURL(a.id), &opts))
}

// OnKeyDown returns a via.h DOM attribute that triggers when a key is pressed.
// key: optional, see https://developer.mozilla.org/en-US/docs/Web/API/KeyboardEvent/key
// Example: OnKeyDown("Enter")
func (a *ActionHandle) OnKeyDown(key string, options ...ActionHandleOption) h.H {
	opts := applyOptions(options...)
	var condition string
	if key != "" {
		condition = fmt.Sprintf("evt.key==='%s' &&", key)
	}
	return h.Data("on:keydown", fmt.Sprintf("%s%s", condition, buildOnExpr(actionURL(a.id), &opts)))
}

// OnInit returns a via.h attribute that triggers after the page loads.
func (a *ActionHandle) OnInit() h.H {
	return h.DataInit(actionURL(a.id))
}
