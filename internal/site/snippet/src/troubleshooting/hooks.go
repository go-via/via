package troubleshooting

import (
	"sync/atomic"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

var online atomic.Int64

type Room struct{ Msgs via.List[string] }

// snippet:start hooks
func (r *Room) OnInit(ctx *via.Ctx) error {
	ctx.OnConnect(r.join)
	ctx.OnDispose(r.leave)
	return nil
}

func (r *Room) join()  { online.Add(1) }
func (r *Room) leave() { online.Add(-1) }

// snippet:end

func (r *Room) View() h.H { return h.Ul(r.Msgs.Each(r.msg)) }

func (r *Room) msg(s string) h.H { return h.Li(h.Str(s)) }
