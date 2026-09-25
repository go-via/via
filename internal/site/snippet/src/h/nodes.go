// Package h holds the compile-checked samples of the /h page. Each sample is a
// func returning a node, and the HTML shown beside it is a constant that
// h_test.go pins to a real render of that func.
package h

import (
	"github.com/go-via/via/h"
)

// snippet:start nodes
func Card(title string, n int) h.H {
	return h.Section(h.Class("card"),
		h.H2(h.Str(title)),
		h.P(h.Str(n), h.Str(" new")),
		h.ID("inbox"),
	)
}

var Inbox = Card("Inbox", 3)

// snippet:end

const CardHTML = `<section class="card" id="inbox">
  <h2>Inbox</h2>
  <p>3 new</p>
</section>`
