package demos

import "github.com/go-via/via/h"

// RouteNotes is documentation, not a reading — like CSPNotes. Every page of
// this site is mounted at a fixed path, so there is no {id} here to read; what
// the unit states is what a parameterised mount does.
type RouteNotes struct{}

type routeRow struct{ code, note string }

var routeRows = []routeRow{
	{`via.Mount(r, "/thread/{id}", Thread{})`,
		"the path is an http.ServeMux pattern. via.Handler(root) is the same thing mounted at \"/\"."},
	{`ctx.Param[int]("id")`,
		"reads one named segment, decoded into T. A segment that will not decode answers 404 rather than a zero value, and naming a segment the pattern does not have panics."},
	{"OnInit, actions, Tick, Listen",
		"where Param is callable. View takes no Ctx, so a param is read in OnInit and kept in a field."},
	{`/thread/{id}/_via/a/{child}/{act}`,
		"an action's address: the mount pattern with its segments filled in, and nothing else on it."},
}

func (r *RouteNotes) View() h.H {
	items := []h.H{h.Class("reflist")}
	for _, row := range routeRows {
		items = append(items, h.Li(h.Code(h.Str(row.code)), h.Str(" — "), h.Str(row.note)))
	}
	return h.Div(
		h.P(h.Str("A mount and the params it reads:")),
		h.Ul(items...),
		h.P(h.Str("The trap follows from the action address. A page's \"?status=open&page=2\" is not on it, "+
			"so the discovery render that decides what is dispatchable runs against the unfiltered page: a row "+
			"that only exists under the filter bound no action in that render, and clicking it answers 410.")),
		h.P(h.Str("Path params survive an action; query params do not. "+
			"ctx.Request().URL.Query() is therefore populated on the GET and empty on every action, which makes "+
			"path segments or the session the place for a page's filter, page number, sort and tab.")),
		h.Pre(h.Code(h.Str("via.Mount(r, \"/tickets/{status}/{page}\", TicketList{}) // survives an action\n"+
			"/tickets?status=open&page=2                            // does not"))),
	)
}
