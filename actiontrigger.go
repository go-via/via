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
// to other nodes in a view.
func (a *actionTrigger) OnClick() h.H {
	return h.Data("on:click", fmt.Sprintf("@get('/_action/%s')", a.id))
}
