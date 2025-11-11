package via

import (
	"fmt"
	"strconv"

	"github.com/go-via/via/h"
)

// actionTrigger represents a trigger to an event handler fn
type actionTrigger struct {
	id string
}

// ActionTriggerOption configures behavior of action triggers
type ActionTriggerOption interface {
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

// WithSignal sets a signal value before triggering the action.
func WithSignal(sig *signal, value string) ActionTriggerOption {
	return withSignalOpt{
		signalID: sig.ID(),
		value:    fmt.Sprintf("'%s'", value),
	}
}

// WithSignalInt sets a signal to an int value before triggering the action.
func WithSignalInt(sig *signal, value int) ActionTriggerOption {
	return withSignalOpt{
		signalID: sig.ID(),
		value:    strconv.Itoa(value),
	}
}

func buildOnExpr(base string, opts *triggerOpts) string {
	if !opts.hasSignal {
		return base
	}
	return fmt.Sprintf("$%s=%s;%s", opts.signalID, opts.value, base)
}

// OnClick returns a via.h DOM attribute that triggers on click. It can be added
// to element nodes in a view.
func (a *actionTrigger) OnClick(options ...ActionTriggerOption) h.H {
	var opts triggerOpts
	for _, opt := range options {
		opt.apply(&opts)
	}
	base := fmt.Sprintf("@get('/_action/%s')", a.id)
	return h.Data("on:click", buildOnExpr(base, &opts))
}

// OnChange returns a via.h DOM attribute that triggers on input change. It can be added
// to element nodes in a view.
func (a *actionTrigger) OnChange(options ...ActionTriggerOption) h.H {
	var opts triggerOpts
	for _, opt := range options {
		opt.apply(&opts)
	}
	base := fmt.Sprintf("@get('/_action/%s')", a.id)
	return h.Data("on:change__debounce.200ms", buildOnExpr(base, &opts))
}

// OnEnterKey returns a via.h DOM attribute that triggers when a key is pressed.
// key: optional, see https://developer.mozilla.org/en-US/docs/Web/API/KeyboardEvent/key
// Example: OnKeyDown("Enter")
func (a *actionTrigger) OnKeyDown(key string, options ...ActionTriggerOption) h.H {
	var opts triggerOpts
	for _, opt := range options {
		opt.apply(&opts)
	}
	var condition string
	if key != "" {
		condition = fmt.Sprintf("evt.key==='%s' &&", key)
	}
	base := fmt.Sprintf("@get('/_action/%s')", a.id)
	return h.Data("on:keydown", fmt.Sprintf("%s%s", condition, buildOnExpr(base, &opts)))
}
