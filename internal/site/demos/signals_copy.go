package demos

import (
	"time"

	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// SignalsCopy copies the command beside its button and flags the button for a
// moment. The class is feedback not worth a signal, so it has none, and the
// text is read off the DOM instead of repeated in the attribute.
type SignalsCopy struct{}

func (c *SignalsCopy) View() h.H {
	return h.Div(h.Class("code"),
		h.Pre(h.Code(h.Str("go get github.com/go-via/via"))),
		h.Button(h.Class("copy"), h.Type("button"),
			on.ClickCS(expr.Do(expr.CopyTextOf("pre"), expr.Class("copied", true))),
			on.ClickCS(expr.Class("copied", false), on.Debounce(1500*time.Millisecond)),
			h.Span(h.Class("icon-clip"), h.Str("Copy")),
			h.Span(h.Class("copy-done"), h.Aria("live", "polite"), h.Str("Copied")),
		),
	)
}
