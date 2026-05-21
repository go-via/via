// Todos exercises a slice of items, list rendering with h.Each, an
// input + signal, StateSess-backed persistence across tabs, and a
// filter signal — without ever leaving Go.
//
//	go run ./internal/examples/todos
package main

import (
	"net/http"
	"slices"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/plugins/picocss"
)

type Item struct {
	Text string
	Done bool
}

type Todos struct {
	Draft  via.SignalStr      `via:"draft"`
	Filter via.StateTabStr    `via:"filter,init=all"`
	Index  via.SignalNum[int] `via:"index"`
	Items  via.StateSessSlice[Item]
}

func (t *Todos) Add(ctx *via.Ctx) error {
	text := strings.TrimSpace(t.Draft.Read(ctx))
	if text == "" {
		return nil
	}
	t.Items.Update(ctx, func(items []Item) []Item {
		return append(items, Item{Text: text})
	})
	t.Draft.Set(ctx, "")
	return nil
}

func (t *Todos) Toggle(ctx *via.Ctx) error {
	idx := t.Index.Read(ctx)
	t.Items.Update(ctx, func(items []Item) []Item {
		if idx < 0 || idx >= len(items) {
			return items
		}
		next := slices.Clone(items)
		next[idx].Done = !next[idx].Done
		return next
	})
	return nil
}

func (t *Todos) Clear(ctx *via.Ctx) error {
	t.Items.Update(ctx, func(items []Item) []Item {
		live := make([]Item, 0, len(items))
		for _, it := range items {
			if !it.Done {
				live = append(live, it)
			}
		}
		return live
	})
	return nil
}

func (t *Todos) View(ctx *via.CtxR) h.H {
	items := t.Items.Read(ctx)
	filter := t.Filter.Read(ctx)
	visible := make([]int, 0, len(items))
	for i, it := range items {
		switch filter {
		case "active":
			if it.Done {
				continue
			}
		case "done":
			if !it.Done {
				continue
			}
		}
		visible = append(visible, i)
	}

	pendingCount := 0
	for _, it := range items {
		if !it.Done {
			pendingCount++
		}
	}

	return h.Body(
		h.Main(h.Class("container"),
			h.H1(h.Text("Todos")),

			// Add is type=button so the wrapping form does not also
			// submit natively and race the datastar action POST.
			h.Form(
				h.Style("display:flex;gap:0.5rem"),
				h.Input(h.Type("text"), t.Draft.Bind(),
					h.Placeholder("What needs doing?")),
				h.Button(h.Type("button"), h.Text("Add"), on.Click(t.Add)),
			),

			// Filter row — three buttons that set the filter state
			// and re-render via the same render path.
			h.Div(h.Style("display:flex;gap:0.5rem;margin:1rem 0"),
				filterButton("all", filter, t.FilterAll),
				filterButton("active", filter, t.FilterActive),
				filterButton("done", filter, t.FilterDone),
				h.Span(h.Style("margin-left:auto"),
					h.Textf("%d remaining", pendingCount)),
			),

			// List
			h.Ul(
				h.Style("padding:0;margin:0"),
				h.Each(visible, func(i int) h.H {
					it := items[i]
					return h.Li(
						h.Style("list-style:none;padding:0.5rem;display:flex;align-items:center;gap:0.5rem"),
						h.Input(h.Type("checkbox"),
							h.If(it.Done, h.Attr("checked")),
							on.Change(t.Toggle, on.SetSignal(&t.Index.Signal, i)),
						),
						h.Span(
							h.Style(strings.Join([]string{
								"flex:1",
								h.IfStr(it.Done, "text-decoration:line-through;opacity:0.5"),
							}, ";")),
							h.Text(it.Text),
						),
					)
				}),
			),

			// Clear-completed only when there's something to clear
			h.If(pendingCount < len(items),
				h.Button(
					h.Class("outline secondary"),
					h.Style("margin-top:1rem"),
					h.Text("Clear completed"),
					on.Click(t.Clear),
				),
			),
		),
	)
}

// filterButton renders one of the three filter pills. Active pill is
// styled with the standard button look; others get the outline class.
func filterButton(name, current string, action any) h.H {
	return h.Button(
		h.Class(h.IfStr(name != current, "outline secondary")),
		h.Text(strings.ToUpper(name[:1])+name[1:]),
		on.Click(action),
	)
}

// Each filter action short-circuits when the user clicks the already-active
// filter — clicking "All" while filter == "all" is a no-op rather than a
// wasted view rebuild + SSE patch.
func (t *Todos) FilterAll(ctx *via.Ctx) {
	if t.Filter.Read(ctx) != "all" {
		t.Filter.Update(ctx, func(string) string { return "all" })
	}
}
func (t *Todos) FilterActive(ctx *via.Ctx) {
	if t.Filter.Read(ctx) != "active" {
		t.Filter.Update(ctx, func(string) string { return "active" })
	}
}
func (t *Todos) FilterDone(ctx *via.Ctx) {
	if t.Filter.Read(ctx) != "done" {
		t.Filter.Update(ctx, func(string) string { return "done" })
	}
}

func main() {
	app := via.New(
		via.WithTitle("Via Todos"),
		via.WithPlugins(picocss.Plugin()),
	)
	via.Mount[Todos](app, "/")
	_ = http.ListenAndServe(":3000", app)
}
