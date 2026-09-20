package demos

import (
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Lifecycle logs its own hooks as they run. The log is a List, which is also
// what makes this unit live, so the list you are reading was pushed to you.
type Lifecycle struct{ Log via.List[string] }

func (l *Lifecycle) OnInit(ctx *via.Ctx) error {
	l.note("OnInit — before the View, on the GET and again on the stream connect")
	ctx.OnConnect(func() { l.note("OnConnect — the stream is open; every Listen has subscribed") })
	return nil
}

func (l *Lifecycle) OnReload(ctx *via.Ctx) error {
	l.note("OnReload — after the action, before the render")
	return nil
}

func (l *Lifecycle) Act(ctx *via.Ctx) { l.note("the action handler") }

func (l *Lifecycle) note(s string) {
	l.Log.Append(time.Now().Format("15:04:05.000") + "  " + s)
	if n := len(l.Log.Get()); n > keepRows {
		l.Log.Set(l.Log.Get()[n-keepRows:])
	}
}

func (l *Lifecycle) View() h.H {
	return h.Div(
		h.Div(h.Class("row"),
			h.Button(via.On("click", l.Act), h.Str("run an action")),
			h.A(h.Href("/platform"), h.Str("reload the page")),
		),
		h.Ol(h.Class("loglist"), l.Log.Each(func(s string) h.H { return h.Li(h.Str(s)) })),
	)
}
