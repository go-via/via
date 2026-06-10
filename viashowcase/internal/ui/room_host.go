package ui

import (
	"encoding/json"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/plugins/echarts"
	"github.com/go-via/via/plugins/maplibre"
	"github.com/go-via/viashowcase/internal/auth"
	"github.com/go-via/viashowcase/internal/core"
	"github.com/go-via/viashowcase/internal/store"
)

// Host is the big-screen owner view: live charts + map driven over SSE.
type Host struct {
	Votes   via.StateAppEvents[core.Vote, core.Tallies]   `via:"votes"`
	QA      via.StateAppEvents[core.QAEvent, core.Boards] `via:"qa"`
	Pins    via.StateAppEvents[core.Pin, core.PinSets]    `via:"pins"`
	Present via.StateApp[map[string]int]                  `via:"present"`

	Code string `path:"code"`
	room store.Room
}

func (p *Host) OnInit(ctx *via.Ctx) error {
	r, err := Deps.DB.RoomByCode(ctx.Request().Context(), p.Code)
	if err != nil {
		ctx.Redirect("/")
		return nil
	}
	u, ok := auth.Current(ctx)
	if !ok || u.ID != r.HostID {
		ctx.Redirect("/")
		return nil
	}
	p.room = r
	return nil
}

func (p *Host) OnConnect(ctx *via.Ctx) error {
	p.bumpPresence(ctx, +1)
	p.push(ctx) // paint once immediately
	via.Stream(ctx, time.Second, func(ctx *via.Ctx, _ time.Time) { p.push(ctx) })
	return nil
}

func (p *Host) OnDispose(ctx *via.Ctx) { p.bumpPresence(ctx, -1) }

func (p *Host) bumpPresence(ctx *via.Ctx, d int) {
	_ = p.Present.Update(ctx, func(m map[string]int) (map[string]int, error) {
		return core.BumpPresence(m, p.Code, d), nil
	})
}

// push streams the current tally to ECharts and the pins to MapLibre.
func (p *Host) push(ctx *via.Ctx) {
	ranked := p.Votes.Read(ctx).For(p.Code).Ranked()
	cats := make([]string, len(ranked))
	data := make([][]any, len(ranked))
	for i, pr := range ranked {
		cats[i] = pr.Choice
		data[i] = []any{pr.Choice, pr.Count}
	}
	_ = Deps.Chart.SetOption(ctx, map[string]any{"xAxis": map[string]any{"data": cats}})
	_ = Deps.Chart.SetSeries(ctx, echarts.Bar("Votes", data, echarts.Color("#ffbf00")))

	pins := p.Pins.Read(ctx).For(p.Code)
	feats := make([]map[string]any, len(pins))
	for i, ll := range pins {
		feats[i] = maplibre.PointFeature(ll.Lng, ll.Lat, nil)
	}
	_ = Deps.Map.SetGeoJSON(ctx, "pins", maplibre.FeatureCollection(feats...))
}

// Notice broadcasts a "starting now" banner to every live tab.
func (p *Host) Notice(ctx *via.Ctx) error {
	Deps.App.Broadcast(buildNoticeScript(p.room.Code, p.room.Title))
	return nil
}

// buildNoticeScript returns the JS snippet broadcast to display the "starting
// now" banner. App.Broadcast queues it on every live tab app-wide, so the
// script gates itself to this room: it early-returns unless the tab's path
// ends with the room code (room URLs are /r/{code} and /host/{code}, so the
// code is always the final segment), keeping the banner off other rooms and
// the home/login pages. endsWith (not indexOf) avoids a substring false match
// since codes are variable-length — a short code must not match a longer
// code's path.
//
// Both the code and the title travel as the JSON-parsed arguments of a
// function-call IIFE — isolated data segments, never dropped between two raw
// JS string fragments. That shape is the only one the CodeQL "potentially
// unsafe quoting" rule accepts for dynamic values bound for an inline
// <script>: the JSON literal closes the surrounding parens, JS line separators
// (U+2028/U+2029) and a literal </script> are neutralised by json.Marshal's
// default HTML/control-character escaping, and the banner text is assigned via
// textContent (the XSS-safe DOM sink) so the value is treated as text.
func buildNoticeScript(code, title string) string {
	codeJSON, _ := json.Marshal(code)
	msg, _ := json.Marshal("▶ Starting now: " + title)
	return `(function(code,msg){if(!location.pathname.endsWith('/'+code)){return;}` +
		`var b=document.createElement('div');b.textContent=msg;` +
		`b.style.cssText='position:fixed;top:0;left:0;right:0;` +
		`z-index:9999;background:#ffbf00;color:#0b0b0f;text-align:center;padding:.8rem;font-weight:700;` +
		`font-family:Inter,system-ui,sans-serif;letter-spacing:.01em;box-shadow:0 6px 24px rgba(255,191,0,.3);` +
		`animation:slideDown .25s ease';document.body.appendChild(b);` +
		`setTimeout(function(){b.remove()},6000)})(JSON.parse(` +
		string(codeJSON) + `),JSON.parse(` + string(msg) + `))`
}

func (p *Host) View(ctx *via.CtxR) h.H {
	live := p.Present.Read(ctx)[p.Code]
	return Shell(ctx, p.room.Title,
		h.Div(h.Class("stage-head"),
			h.Div(h.Class("stage-meta"),
				h.Span(h.Class("live-pill"), h.Textf("LIVE · %d watching", live)),
				h.A(h.Href("/r/"+p.Code), h.Class("join-code"), h.Text("/r/"+p.Code)),
			),
			h.Button(h.Class("outline"), h.Text("📢 Broadcast “starting now”"), on.Click(p.Notice)),
		),
		h.Div(h.Class("host-panels"),
			h.Article(h.Class("stage-panel"),
				h.H3(h.Class("panel-title"), h.Text("Live results")),
				// Empty state before any votes — the bare chart grid looks unfinished.
				// Reading Votes here re-renders the panel, so it vanishes on first vote.
				h.When(p.room.Kind != "qa" && p.Votes.Read(ctx).For(p.Code).Total() == 0, func() h.H {
					return h.P(h.Class("muted", "empty-hint"),
						h.Text("Waiting for the first vote — share the link to get started."))
				}),
				Deps.Chart.Mount(),
				// Text alternative for the canvas chart: screen readers can't read the
				// chart, so mirror the ranked tally into a visually-hidden live region.
				// Reading Votes here also re-renders this view as votes fold.
				h.When(p.room.Kind != "qa", func() h.H {
					return h.Ul(h.Class("sr-only"), h.Attr("aria-live", "polite"),
						h.Attr("aria-label", "Live results"),
						h.Each(p.Votes.Read(ctx).For(p.Code).Ranked(), func(pr core.Pair) h.H {
							return h.Li(h.Textf("%s: %d votes", pr.Choice, pr.Count))
						}))
				}),
			),
			h.Article(h.Class("stage-panel"),
				h.H3(h.Class("panel-title"), h.Text("Where the room is")),
				Deps.Map.Mount(),
			),
			h.When(p.room.Kind == "qa", func() h.H {
				qs := p.QA.Read(ctx).For(p.Code)
				return h.Article(h.Class("stage-panel"),
					h.H3(h.Class("panel-title"), h.Text("Questions")),
					h.When(len(qs) == 0, func() h.H {
						return h.P(h.Class("muted", "empty-hint"), h.Text("No questions yet — they'll appear here as the room asks."))
					}),
					h.Ul(h.Class("qa-board"), h.Each(qs, func(q core.Question) h.H {
						return h.Li(
							h.Span(h.Class("qa-votes"), h.Textf("▲ %d", q.Votes)),
							h.Span(h.Text(q.Text)),
						)
					})),
				)
			}),
		),
	)
}
