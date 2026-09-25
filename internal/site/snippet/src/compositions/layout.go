package compositions

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// snippet:start shell
type Shell[C any] struct {
	Title string
	Body  C
}

func (s *Shell[C]) PageMeta() via.Meta { return via.Meta{Title: s.Title} }

func (s *Shell[C]) View() h.H {
	return h.Div(
		h.Header(h.H1(h.Str(s.Title))),
		h.Main(via.Child(s.Body)),
	)
}

// snippet:end

// snippet:start slots
type TwoColumn[S, M any] struct {
	Side S
	Main M
}

func (t *TwoColumn[S, M]) View() h.H {
	return h.Div(h.Class("cols"),
		h.Aside(via.Child(t.Side)),
		h.Section(via.Child(t.Main)),
	)
}

// snippet:end

// MountShells mounts one Shell per page.
// snippet:start mount
func MountShells(r *via.Router) {
	// snippet:start settings
	vol := Stepper{Label: "volume"}
	via.Mount(r, "/settings",
		Shell[Stepper]{
			Title: "Settings",
			Body:  vol,
		})
	// snippet:end
	via.Mount(r, "/inbox",
		Shell[TwoColumn[Profile, Inbox]]{
			Title: "Inbox",
		})
}

// snippet:end
