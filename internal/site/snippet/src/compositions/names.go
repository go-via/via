package compositions

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// snippet:start names
type Chat struct {
	Draft via.Signal[string]
}

type Inbox struct {
	Chat Chat // Draft is $chat__draft; container #via-i0
}

type Split struct {
	Left, Right Inbox // $i0__chat__draft, $i1__chat__draft
}

func (c *Chat) View() h.H  { return h.Input(c.Draft.Bind()) }
func (i *Inbox) View() h.H { return h.Div(via.Child(i.Chat)) }
func (s *Split) View() h.H { return h.Div(via.Child(s.Left), via.Child(s.Right)) }

// snippet:end
