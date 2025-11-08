package via

import (
	"fmt"

	"github.com/go-via/via/h"
)

// actionTrigger represents a trigger to an event handler fn
type actionTrigger struct {
	id string
}

// OnClick returns a via.h DOM node that triggers on click. It can be added
// to element nodes in a view.
func (a *actionTrigger) OnClick() h.H {
	return h.Data("on:click", fmt.Sprintf("@get('/_action/%s')", a.id))
}

// OnChange returns a via.h DOM node that triggers on input change. It can be added
// to element nodes in a view.
func (a *actionTrigger) OnChange() h.H {
	return h.Data("on:change__debounce.200ms", fmt.Sprintf("@get('/_action/%s')", a.id))
}

// OnEnterKey returns a via.h DOM node that triggers when Enter key is pressed.
func (a *actionTrigger) OnEnterKey() h.H {
	return h.Data("on:keydown", fmt.Sprintf("(evt.code==='Enter') && @get('/_action/%s')", a.id))
}
