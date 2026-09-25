package content

import (
	"crypto/rand"
	"io/fs"
	"log/slog"
	"net/http/httptest"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/snippet"
	"go-via.dev/site/snippet/src/islands"
	"go-via.dev/site/static"
)

// Islands is the page on handing a subtree to a JS library.
type Islands struct {
	page
	// Assets decides the mount's CSP and via reads it at Mount, so it is built
	// once in NewIslands and never per request. via.Assets is a foreign struct,
	// which the Mount probe does not fill, so the probe reads it as set.
	assets via.Assets
	Spark  demos.Sparkline
	Maps   demos.MapGrid
	Chart  demos.Chart
}

// NewIslands builds the islands page, with its scripts under the site's base.
func NewIslands(env Env) Islands {
	return Islands{
		page: newPage("/islands", env),
		assets: via.Assets{
			Scripts: []via.Script{
				demo.Script(env.Site.Asset),
				{Src: env.Site.Asset("/static/vendor/maplibre-gl-csp.js"), Defer: true},
				{Src: env.Site.Asset("/static/islands.js"), Defer: true},
			},
			Styles: []via.Style{{Href: env.Site.Asset("/static/islands.css")}},
		},
	}
}

func (p *Islands) PageMeta() via.Meta {
	m := p.meta("Handing a subtree to a JS library: DataIgnoreMorph, DataEffect, via:remove teardown, OnDispose, and the CSP a CDN script adds.")
	m.Assets = p.assets
	return m
}

// islandsJS cuts one statement out of static/islands.js, from the line that
// starts with open to the next line equal to end, so the page shows the file
// the demos run. A missing anchor panics at startup.
func islandsJS(open, end string) string {
	b, err := fs.ReadFile(static.FS, "islands.js")
	if err != nil {
		panic(err)
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if out == nil && !strings.HasPrefix(strings.TrimSpace(l), open) {
			continue
		}
		out = append(out, strings.TrimPrefix(l, "  "))
		if l == end {
			return strings.Join(out, "\n") + "\n"
		}
	}
	panic("content: static/islands.js has no block " + open + " … " + end)
}

// gaugeCSP is the header via serves for islands.Gauge on a router with no
// WithHead assets. buildCSP is unexported, so it is read off a real mount
// instead of retyped. The key only exists because a router needs one.
func gaugeCSP() string {
	key := make([]byte, 32)
	rand.Read(key)
	r := via.NewRouter(via.WithSessionKey(key), via.WithLogger(slog.New(slog.DiscardHandler)))
	defer r.Close()
	via.Mount(r, "/", islands.Gauge{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		panic("content: the Gauge mount served no Content-Security-Policy")
	}
	return strings.ReplaceAll(csp, "; ", ";\n")
}

var (
	jsMap      = islandsJS("window.viaMap = ", "  };")
	jsTeardown = islandsJS(`document.addEventListener("via:remove"`, "  });")
	cdnCSP     = gaugeCSP()
)

func (p *Islands) View() h.H {
	d := p.doc()
	return d.Page(
		h.P(
			h.Str("via never generates JavaScript. An island is a container via renders once and then leaves to a library of yours: "),
			h.Str("Go feeds it by writing signals, and your script draws."),
		),

		d.H2("The island contract"),
		Steps(
			Step("Mark the container",
				h.P(API("h.DataIgnoreMorph"), h.Str(" keeps every later patch out of the subtree, so the DOM the library built survives re-renders of the unit around it."))),
			Step("Make init idempotent",
				h.P(API("h.DataEffect"), h.Str(" re-runs on every change of every signal it reads. Build the library instance once, keyed on state stored on the element ("),
					Code("if (!el._map)"), h.Str("), and on later runs only update it."))),
			Step("Tear down on via:remove",
				h.P(h.Str("via dispatches "), Code("via:remove"), h.Str(" on "), Code("document"), h.Str(" for each element that leaves the page, with the removed root in "),
					Code("e.detail.el"), h.Str(". Free WebGL contexts, workers and observers there; via does not know the library holds them."))),
			Step("Release server-side producers in OnDispose",
				h.P(API("via.Ctx.OnDispose"), h.Str(" runs when the unit's connection closes. Whatever feeds the island from outside the unit stops there."))),
			Step("Declare every script in Assets",
				h.P(h.Str("Scripts and styles go in the "), Code("Assets"), h.Str(" field of the root's "), API("via.Meta"),
					h.Str(", which decides the page's CSP. A relative URL is covered by "), Code("'self'"),
					h.Str("; an absolute one adds its origin. It must be a constant of the type: via reads it at Mount and panics if a render disagrees."))),
		),

		d.H2("Feeding an island from Go"),
		demo.Card(d.H3("Sparkline on a clock"),
			h.P(
				h.Str("A "), API("via.Ctx.Tick"), h.Str(" appends a sample and calls "), API("via.Signal.Set"),
				h.Str(" on a "), Code("Signal[[]int]"), h.Str(" the View never binds or displays. Set is what declares the signal, so the value reaches the browser anyway, and "),
				Code("viaChart"), h.Str(" in "), Code("static/islands.js"), h.Str(" redraws the canvas each time the effect re-runs."),
			),
			via.Child(p.Spark), "sparkline.go",
			demo.WireOpen(),
			demo.Try(h.Str("Watch the Wire pane: one patch a second, each carrying the whole series as a signal."),
				h.Span(h.Str("Open Source: the canvas has "), Code("data-ignore-morph"), h.Str(" and a "), Code("data-effect"), h.Str(" calling "), Code("viaChart"), h.Str(".")))),

		d.H2("The JavaScript half"),
		snippet.Text("static/islands.js", jsMap),
		h.P(h.Str("The first run builds the map and stores it on the element; every later run finds "), Code("el._map"),
			h.Str(" and only calls "), Code("jumpTo"), h.Str(". The effect passes "), Code("el"), h.Str(" and the signal's value as arguments, from "),
			API("expr.Call"), h.Str(" with "), API("expr.El"), h.Str(" and "), API("via.Signal.Ref"), h.Str(".")),
		snippet.Text("static/islands.js", jsTeardown),
		h.P(h.Str("A removed subtree can hold many islands, so the listener checks the root and everything under it.")),

		d.H2("Many instances, one script"),
		demo.Card(d.H3("Six maps, one script"),
			h.P(
				h.Str("The MapLibre script is declared once in the page's Assets and serves however many maps the grid holds. "),
				h.Str("The grid is one unit with a fixed Signal per slot, not a child per map; "),
				h.A(h.Href(d.Href("/compositions")), h.Str("Compositions")),
				h.Str(" covers why a child's key must not move."),
			),
			via.Child(p.Maps), "mapgrid.go",
			demo.Try(h.Str("Shuffle: the maps jump, none is rebuilt."),
				h.Span(h.Str("Add map, then Remove last: the "), Code("via:remove"), h.Str(" listener calls "), Code("_map.remove()"), h.Str(" on the map that left.")))),
		Callout(Note, "Same-origin by construction",
			h.P(h.Str("MapLibre's CSP build, its worker, the GeoJSON and a style with no tiles, glyphs or sprite are all served from "),
				Code("/static/"), h.Str(", so this page's "), Code("default-src 'self'"), h.Str(" needs no widening."))),

		d.H2("One signal per action"),
		demo.Card(d.H3("The same island, no clock"),
			h.P(
				h.Str("The same "), Code("viaChart"), h.Str(", driven by a click. This unit never ticks and holds no State, so the POST's own response answers it: one element patch whose "),
				Code("data-signals"), h.Str(" carries the one signal the handler wrote. The effect re-runs on arrival."),
			),
			via.Child(p.Chart), "chart.go",
			demo.WireOpen(),
			demo.Try(h.Span(h.Str("Click New data, then open the newest Wire entry, the "), Code("patch-elements"), h.Str(" response: its "),
				Code("data-signals"), h.Str(" holds "), Code("chart__series"), h.Str(" and nothing else.")))),

		d.H2("Stopping a producer on disconnect"),
		snippet.Region("islands/dispose.go", "dispose", snippet.Title("prices.go"), snippet.Mark("OnConnect", "OnDispose")),
		h.P(API("via.Ctx.OnConnect"), h.Str(" and "), API("via.Ctx.OnDispose"), h.Str(" register in OnInit and run once per connection. "),
			h.Str("OnInit runs on every request that renders the unit, so an acquire there would fire for requests that never open a stream.")),
		Callout(Warning, "Only on a live unit",
			h.P(h.Str("OnDispose runs when a connection closes. A unit that never ticks, listens or displays State opens none, so its OnDispose never runs."))),

		d.H2("Libraries from another origin"),
		snippet.Region("islands/cdn.go", "cdn", snippet.Title("gauge.go"), snippet.Mark("https://cdn")),
		h.P(h.Str("Mounted on a router with no other assets, that type is served this policy. The text below is the header via returned when this site started, one directive per line:")),
		snippet.Text("Content-Security-Policy", cdnCSP),
		h.P(h.Str("The CDN origin joins "), Code("script-src"), h.Str(" and nothing else changes. The three hashes are via's own inline scripts. "),
			h.A(h.Href(d.Href("/security")+"#content-security-policy"), h.Str("Sessions & security")), h.Str(" goes through the policy directive by directive.")),
		Callout(Caveat, "No integrity attribute",
			h.P(API("via.Script"), h.Str(" has no field for Subresource Integrity, so a CDN script is trusted by origin alone. Serving the file yourself keeps the policy at "),
				Code("'self'"), h.Str(" and the bytes pinned."))),

		Callout(Note, "Coming from v0.7 plugins",
			h.P(h.Str("The echarts and maplibre plugins passed to "), Code("WithPlugins"), h.Str(" are gone. Each becomes an island: the library's script in Assets, a container with "),
				API("h.DataIgnoreMorph"), h.Str(" and "), API("h.DataEffect"), h.Str(", and a function of yours. "),
				h.A(h.Href(d.Href("/migrate")), h.Str("Migrating from v0.7")), h.Str(" has the full mapping."))),
	)
}
