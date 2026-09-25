package demos

import (
	"math/rand/v2"
	"slices"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
)

// Sparkline feeds a canvas JavaScript owns from Go. Load is a Signal nothing
// renders: Set declares the slot, so every tick's patch carries the series to
// the island without a Bind or a Display. The tag starts it at an empty array,
// so the effect never sees the null a nil slice would marshal to.
type Sparkline struct {
	Load via.Signal[[]int] `via:"init=[]"`
	load []int
}

func (s *Sparkline) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Second, s.beat)
	return nil
}

func (s *Sparkline) beat(ctx *via.Ctx) {
	s.load = append(s.load, 20+rand.IntN(80))
	if len(s.load) > 40 {
		s.load = s.load[1:]
	}
	// Clone: Set keeps the slice header, and the next beat's append would
	// otherwise write through it into the value already on the wire.
	s.Load.Set(slices.Clone(s.load))
}

func (s *Sparkline) View() h.H {
	// DataIgnoreMorph keeps the node across live patches, so the bitmap the
	// effect drew survives.
	return h.Canvas(h.Class("chart"), h.DataIgnoreMorph(),
		h.Width(420), h.Height(90),
		// A canvas is opaque to a screen reader; role and label are the only
		// description of it there is.
		h.Role("img"),
		h.Aria("label", "Line chart of the last 40 load samples, one a second"),
		h.DataEffect(expr.Call("viaChart", expr.El, s.Load.Ref())))
}
