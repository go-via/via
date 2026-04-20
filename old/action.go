package via

import (
	"fmt"
	"strings"

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

// signalIDer is satisfied by any signal type that exposes a display ID.
type signalIDer interface {
	displayID() string
}

// ActionWithSetSignal sets a signal value before triggering the action.
// Type-safe: value T must match the signal's type.
func ActionWithSetSignal[T any](sig signalIDer, value T) ActionTriggerOption {
	var strVal string
	switch v := any(value).(type) {
	case string:
		strVal = fmt.Sprintf("'%s'", strings.ReplaceAll(v, "'", "\\'"))
	default:
		strVal = fmt.Sprintf("%v", v)
	}
	return withSignalOpt{
		signalID: sig.displayID(),
		value:    strVal,
	}
}

func buildOnExpr(base string, opts *triggerOpts) string {
	if !opts.hasSignal {
		return base
	}
	return fmt.Sprintf("$%s=%s;%s", opts.signalID, opts.value, base)
}

func applyOptions(options ...ActionTriggerOption) triggerOpts {
	var opts triggerOpts
	for _, opt := range options {
		opt.apply(&opts)
	}
	return opts
}

func actionURL(id string) string {
	return fmt.Sprintf("@post('/_action/%s')", id)
}

// OnClick returns a via.h DOM attribute that triggers on click. It can be added
// to element nodes in a view.
func (a *actionTrigger) OnClick(options ...ActionTriggerOption) h.H {
	opts := applyOptions(options...)
	return h.Data("on:click", buildOnExpr(actionURL(a.id), &opts))
}

// OnChange returns a via.h DOM attribute that triggers on input change. It can be added
// to element nodes in a view.
func (a *actionTrigger) OnChange(options ...ActionTriggerOption) h.H {
	opts := applyOptions(options...)
	return h.Data("on:change__debounce.200ms", buildOnExpr(actionURL(a.id), &opts))
}

// OnKeyDown returns a via.h DOM attribute that triggers when a key is pressed.
// key: optional, see https://developer.mozilla.org/en-US/docs/Web/API/KeyboardEvent/key
// Example: OnKeyDown("Enter")
func (a *actionTrigger) OnKeyDown(key string, options ...ActionTriggerOption) h.H {
	opts := applyOptions(options...)
	var condition string
	if key != "" {
		condition = fmt.Sprintf("evt.key==='%s' &&", strings.ReplaceAll(key, "'", "\\'"))
	}
	return h.Data("on:keydown", fmt.Sprintf("%s%s", condition, buildOnExpr(actionURL(a.id), &opts)))
}
