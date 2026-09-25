// Package signals holds the compile-checked samples of the /signals page.
package signals

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// snippet:start names
type Chat struct {
	Draft via.Signal[string] // $chat__draft
}

type Inbox struct {
	Count via.Signal[int]    // $count
	Open  via.SignalCS[bool] // $_open
	Form  struct {
		Email via.Signal[string] // $form_email
	}
	Chat Chat // rendered with via.Child(p.Chat)
}

// snippet:end

func (c *Chat) View() h.H { return h.Input(c.Draft.Bind()) }

func (p *Inbox) View() h.H {
	return h.Div(
		p.Count.Display(),
		p.Open.Display(),
		h.Input(p.Form.Email.Bind()),
		via.Child(p.Chat),
	)
}
