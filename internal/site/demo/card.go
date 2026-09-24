package demo

import "github.com/go-via/via/h"

// emptyPane is also inspector.js's EMPTY: the script rewrites the pane and
// puts this back when the log is cleared. demo/card_test.go pins the pair.
const emptyPane = "Interact with the demo to see requests, frames and signals here."

// Card renders a running demo followed by its Inspector and Source, each in a
// <details> that starts closed, so the card needs no script or signal to open
// them. inspector.js finds a pane's demo through the enclosing .demo.
func Card(title string, prose h.H, child h.H, srcName string) h.H {
	return h.Article(h.Class("demo"),
		h.Header(h.H3(h.Str(title))),
		h.Div(h.Class("demo-prose"), prose),
		h.Div(h.Class("demo-live"), child),
		h.Details(h.Class("demo-more"),
			h.Summary(h.Str("Inspector")),
			h.Div(h.Class("inspector"), h.DataIgnoreMorph(), h.Data("inspector", ""),
				h.P(h.Class("insp-empty"), h.Str(emptyPane))),
		),
		h.Details(h.Class("demo-more"),
			h.Summary(h.Str("Source")),
			Source(srcName),
		),
	)
}
