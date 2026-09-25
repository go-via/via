package h

import (
	"github.com/go-via/via/h"
)

// snippet:start attrs
func Qty(max int, sold bool) h.H {
	return h.Input(
		h.Type("number"), h.Name("qty"),
		h.Min(1), h.Max(max),
		h.Value(1),
		h.Disabled(sold),
		h.Required(true),
		h.Class("qty", ""),
		h.Aria("label", "Quantity"),
		h.RawAttr("list", "sizes"),
	)
}

var InStock = Qty(5, false)

// snippet:end

const QtyHTML = `<input type="number" name="qty"
  min="1" max="5" value="1"
  required
  class="qty"
  aria-label="Quantity"
  list="sizes">`
