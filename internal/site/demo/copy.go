package demo

import (
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// withCopy puts a copy button after a code block. The text is read off the
// DOM with Raw rather than passed as expr.Lit: Datastar rewrites "@name(" even
// inside string literals, so a sample containing @post( would be corrupted,
// and a literal would repeat every block's text in an attribute.
func withCopy(pre h.H) h.H {
	return h.Div(h.Class("code"),
		pre,
		h.Button(h.Class("copy"), h.Type("button"), h.Aria("label", "Copy code"),
			on.ClickCS(expr.Copy(expr.Raw("el.previousElementSibling.textContent"))),
			h.Str("Copy")),
	)
}
