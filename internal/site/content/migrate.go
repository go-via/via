package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
)

const counterV07 = `type Counter struct {
    Hits via.StateTabNum[int]
    Step via.SignalNum[int] ` + "`" + `via:"step,init=1"` + "`" + `
}

func (c *Counter) Inc(ctx *via.Ctx) { c.Hits.Op(ctx).Add(c.Step.Read(ctx)) }

func (c *Counter) View(ctx *via.CtxR) h.H {
    return h.Main(h.Class("container"),
        h.P(h.Text("Count: "), c.Hits.Text(ctx)),
        h.Input(h.Type("number"), c.Step.Bind()),
        h.Button(h.Text("+"), on.Click(c.Inc)),
    )
}`

const counterV08 = `type Counter struct{ count *Store } // your type, not via's

func (c *Counter) Inc(ctx *via.Ctx) { c.count.Add(1) }

func (c *Counter) View() h.H {
    return h.Main(h.Class("container"),
        h.P(h.Str("Count: "), h.Str(c.count.Value())),
        h.Button(on.Click(c.Inc), h.Str("+")),
    )
}

func main() {
    http.Handle("/", via.Handler(Counter{count: &Store{}}))
}`

var migration = []row{
	{"via.New(), via.Mount[Page]", `via.Handler(Page{}), or via.NewRouter() and via.Mount(r, "/p", Page{})`},
	{"View(ctx *via.CtxR) h.H", "View() h.H"},
	{"StateTab[T]", "State[T]; rendering it makes the unit live"},
	{"StateSess[T]", "a topic per Session.ID(), followed with State.Track in OnInit"},
	{"StateApp[T]", "your own store, injected; a topic and via.StateTrack to push its changes"},
	{"*Num shapes, .Op(ctx)", "Go arithmetic on Get()"},
	{"Read / Write / Update", "Get / Set"},
	{"StateTab[[]E] + Update", "via.List[E] with Append and Remove"},
	{"Connector.OnConnect, Disposer.Dispose", "a Tick or Listen in OnInit, or a rendered State; ctx.OnConnect and ctx.OnDispose for per-connection work"},
	{"sig.Text(), .TextSpan(), .Show(), .Class()", "Bind() and Display(); the rest are gone"},
	{`h.Text("x")`, `h.Str("x")`},
	{"h.If", "via.When"},
	{"h.Group", "pass the children directly"},
	{"sess.Put/Get/Clear/Rotate", "ctx.Session().Put(v), Get[T](), Delete(), Rotate()"},
	{"app.Broadcast*", "topic.New[T] and ctx.Listen"},
	{"theme options, plugins/picocss", "via.WithHead(via.Head{…}) and your own CSS"},
	{"WithHead{Title}", "PageMeta().Title"},
	{"WithInsecureOrigin", "gone: the origin check is off until WithTrustedOrigin turns it on"},
}

// Migrate is MIGRATION.md cut down to what a v0.7 app has to change.
type Migrate struct{ page }

func NewMigrate(env Env) Migrate { return Migrate{page: newPage("/migrate", env)} }

func (p *Migrate) PageMeta() via.Meta {
	return p.meta("Moving a v0.7 app to v0.8: the four shifts, the v0.7 to v0.8 mapping, what was removed, and the wire breaks.")
}

func (p *Migrate) View() h.H {
	d := p.doc()
	return d.Page(
		h.P(h.Str("v0.8 is a rebuild. The module never left v0.x, so go get -u moves v0.7 to v0.8 as an "+
			"ordinary bump and nothing warns you. Most v0.7 code does not port line by line, because the ideas "+
			"changed, not only the spellings. v0.7 is preserved on the v1 branch.")),

		d.H2("Short version"),
		h.Ul(
			h.Li(h.Str("Delete the View(ctx) parameter.")),
			h.Li(h.Str("Drop .Op(ctx), and replace Read and Write with Get and Set.")),
			h.Li(h.Str("Keep package on: on.Click(p.Inc) reads the same, and modifiers are options.")),
			h.Li(h.Str("Replace h.Text with h.Str. The typed attributes (h.Class, h.Type, h.Style, …) still exist; "+
				"h.RawAttr covers the rest.")),
			h.Li(h.Str("Every via.X(ctx, …) is now ctx.X(…): Param, Redirect, Listen, and "+
				"Session().Put, Get, Delete and Rotate.")),
			h.Li(h.Str("r.Mount(…) is via.Mount(r, …).")),
		),
		h.P(h.Str("The compiler finds the rest.")),

		d.H2("The four shifts"),
		d.H3("State is bare; ctx is for the request"),
		h.P(h.Str("Mutators take no ctx: c.hits.Set(c.hits.Get() + c.step.Get()). ctx is the request, used for "+
			"sessions, path params and subscriptions. via.State[T] is per connection and rendering one makes "+
			"its unit live, so the page streams. For a value every visitor shares, inject your own store and "+
			"let the re-render read it.")),
		d.H3("View is pure and takes no context"),
		h.P(h.Str("Anything a view needs is a field before View runs. OnInit(*via.Ctx) error loads it, on every "+
			"request, and can fail: via.ErrNotFound answers 404, any other error 500. The hooks are duck-typed, "+
			"so Mount panics on a hook name with the wrong signature and warns on a near-miss name that has "+
			"the right one.")),
		d.H3("Composition is via.Child, and roots are taken by value"),
		h.P(h.Str("Slot, Child[C], NewChild, Fill and .Embed are gone. A child is a plain struct field rendered "+
			"with via.Child(p.Sidebar). via.Handler and via.Mount take the root by value, Counter{…} and not "+
			"&Counter{…}. A live child may sit under plain ancestors; a live child inside another live one "+
			"panics at render.")),
		d.H3("Fan-out is scoped to a topic"),
		h.P(h.Str("app.Broadcast and its siblings are gone. Publish on a topic.Topic[T] from anywhere and "+
			"subscribe with ctx.Listen in OnInit; the subscription ends with the child. Nothing can push to a "+
			"page that did not ask.")),

		d.H3("The counter, both ways"),
		h.P(h.Str("v0.7, with reactive per-tab state and a bound signal:")),
		demo.Code(counterV07),
		h.P(h.Str("v0.8, where the count is an injected dependency, the view is pure, and the re-render is the "+
			"update:")),
		demo.Code(counterV08),

		d.H2("Mapping"),
		table([]string{"v0.7", "v0.8"}, mappingRows()...),

		d.H2("Removed outright"),
		h.Ul(
			h.Li(h.Str("Plugins, including plugins/picocss. Styling is your own CSS, delivered through WithHead.")),
			h.Li(h.Str("Theme options and WithoutSSEReconnect. The reconnect manager is always on.")),
			h.Li(h.Str("The sess subpackage.")),
			h.Li(h.Str("The numeric shapes and .Op(ctx).")),
			h.Li(h.Str("Broadcast, BroadcastSignal, BroadcastSignals and BroadcastNotify.")),
			h.Li(h.Str("The h render plumbing: Dyn, DynAttr, NewRenderer, Renderer and Binder.")),
			h.Li(h.Str("via.OnUpload and via.File. via.PostForm is always multipart; read a file with "+
				"ctx.Request().FormFile(name).")),
			h.Li(h.Str("RequireSession and Mount's guards. The check moves into the page's OnInit.")),
			h.Li(h.Str("Most SSE knobs. WithMaxSSEConn and WithPinnedDeadline are the two left as options.")),
		),

		d.H2("Wire breaks"),
		h.P(h.Str("Nothing in your code builds these, so there is nothing to port. A tab left open across the "+
			"upgrade fails once and comes back correct on reload.")),
		h.Ul(
			h.Li(h.Str("Action URLs are /_via/a/{child}/{id}, where id is a hash of the handler's Go name. "+
				"A stale tab's first click answers 410.")),
			h.Li(h.Str("The tab id rides as the signal viatab, not the X-Via-Tab header.")),
			h.Li(h.Str("A signal's wire name is its field name, first letter lower-cased: count, and "+
				"chat__draft inside an embedded Chat.")),
		),

		d.H2("New startup panics"),
		h.Ul(
			h.Li(h.Str("A Signal that is not a plain field of its composition — reached through a pointer, "+
				"slice, array or map, or held by a type whose View has a value receiver — panics at Mount or Child.")),
			h.Li(h.Str("Two fields that mint the same slot name panic: a nested A.B (a_b) next to a field A_b.")),
			h.Li(h.Str("A Mount path with a {name...} or {$} wildcard, or one named {child} or {act}, panics, "+
				"and so does mounting both /docs and /docs/.")),
		),

		d.H2("Staying on v0.7"),
		h.P(h.Str("The v1 branch is preserved and its tags still resolve:")),
		demo.Plain("go get github.com/go-via/via@v0.7.0"),
		h.P(h.Str("v0.7 is frozen: no features, and no commitment to backport security fixes. Pinning it means "+
			"taking on its dependency maintenance yourself.")),

		h.P(h.Str("The full text, including the security defaults that moved and the rough edges in v0.8, is "),
			h.A(h.Href(repo+"MIGRATION.md"), h.Str("MIGRATION.md")), h.Str(" on GitHub.")),
	)
}

func mappingRows() [][]h.H {
	rows := make([][]h.H, 0, len(migration))
	for _, r := range migration {
		rows = append(rows, []h.H{h.Code(h.Str(r.name)), h.Str(r.use)})
	}
	return rows
}
