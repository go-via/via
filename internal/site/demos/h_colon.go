package demos

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// HColon writes one Datastar attribute three ways. The first two render
// data-attr:disabled and work; the third renders data-attr-disabled, which
// Datastar reads as a plugin named "attr-disabled" that does not exist, and
// ignores without a console error.
type HColon struct {
	Draft via.SignalCS[string]
}

func (c *HColon) View() h.H {
	empty := c.Draft.Ref().Eq("")
	return h.Div(h.Class("row"),
		h.Input(c.Draft.Bind(), h.Placeholder("Type to enable")),
		h.Button(h.Type("button"), h.DataAttr("disabled", empty), h.Str("DataAttr")),
		h.Button(h.Type("button"), h.Data("attr:disabled", string(empty)), h.Str(`Data("attr:…")`)),
		h.Button(h.Type("button"), h.Data("attr-disabled", string(empty)), h.Str(`Data("attr-…")`)),
	)
}
