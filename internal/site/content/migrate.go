package content

import (
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/shell"
)

// counterV07 is exact v0.7.0 API, the v0.7 twin of demos/counter.go. It is
// display text: this module cannot import v0.7.
const counterV07 = `type Counter struct{ N via.StateTabNum[int] }

func (c *Counter) Inc(ctx *via.Ctx) { c.N.Op(ctx).Inc() }
func (c *Counter) Dec(ctx *via.Ctx) { c.N.Op(ctx).Dec() }

func (c *Counter) View(ctx *via.CtxR) h.H {
	return h.Div(h.Class("row"),
		h.Button(on.Click(c.Dec), h.Text("−")),
		h.Span(c.N.Text(ctx)),
		h.Button(on.Click(c.Inc), h.Text("+")),
	)
}

func main() {
	app := via.New()
	via.Mount[Counter](app, "/")
	app.Start()
}`

const (
	byCompiler = "compiler"
	byPanic    = "panic at Mount"
	byWarn     = "warning at Mount, or silent"
	bySilent   = "silent"
)

// change is one row of the mapping. Backticks mark code, so MIGRATION.md's
// table is these rows verbatim.
type change struct{ v07, v08, caught string }

// Ordered by how early a port hits them, not by package.
var changes = []change{
	{"`h.Text(s)`, `h.T(s)`, `h.Textf(f, …)`", "`h.Str(s)`, `h.Str(fmt.Sprintf(f, …))`", byCompiler},
	{"`View(ctx *via.CtxR) h.H`", "`View() h.H`; load what it reads into fields in `OnInit`", byCompiler},
	{"`via.New()`, `via.Mount[Page](app, \"/\")`", "`via.Handler(Page{})`, or `via.NewRouter()` and `via.Mount(r, \"/\", Page{})`", byCompiler},
	{"`app.Start()`, `app.Run()`, `WithAddr`, the timeout options", "`http.ListenAndServe(addr, r)`, or your own `http.Server`", byCompiler},
	{"`StateTab[T]` and its `Num`, `Str`, `Bool`, `Slice`, `Map` shapes", "`State[T]`; rendering one makes the unit live", byCompiler},
	{"`.Read(ctx)`, `.Write(ctx, v)`, `.Update(ctx, fn)`", "`.Get()`, `.Set(v)`", byCompiler},
	{"`.Op(ctx).Inc()`, `.Add(n)`, `.Toggle()`, …", "Go on the value: `s.Set(s.Get() + n)`", byCompiler},
	{"`s.Text(ctx)`, `sig.Text()`, `sig.TextSpan()`", "`s.Display()`", byCompiler},
	{"`SignalNum[T]` and the other `Signal` shapes", "`Signal[T]`", byCompiler},
	{"`via:\"name,init=v\"` field tag", "`via:\"init=<json>\"`; the wire name is the field name, and a string seed is JSON: `init=\"all\"`", byPanic},
	{"a child rendered by hand: `p.A.View(ctx, …)`", "`via.Child(p.A)`; what the child's `View` took as arguments becomes its fields", byCompiler},
	{"an action `func(*via.Ctx) error`, `WithActionErrorHandler`", "`func(*via.Ctx)`; handle the error inside", byCompiler},
	{"an `OnInit` error, logged while the page renders anyway", "an `OnInit` error aborts: `via.ErrNotFound` answers 404, any other 500", bySilent},
	{"`path:\"id\"` field tag", "`ctx.Param[T](\"id\")` in `OnInit`, stored in a field", bySilent},
	{"`query:\"q\"` field tag", "`ctx.Request().URL.Query()` in `OnInit`; it is empty on actions, so list state belongs in the path or the session", bySilent},
	{"`h.If(cond, node)`", "`via.When(cond, p.part)`: the node becomes a method returning `h.H`, called only when cond holds", byCompiler},
	{"`h.When(cond, fn)`", "`via.When(cond, fn)`", byCompiler},
	{"`h.IfElse`, `h.WhenElse`, `h.Switch`, `h.Maybe`", "a Go `if` or `switch` in a method returning `h.H`", byCompiler},
	{"`h.Each(items, fn)`, `h.EachIndexed`, `h.EachSeq`", "`via.Each(items, p.row)`, or a loop", byCompiler},
	{"`h.Fragment(…)`", "pass the nodes to the parent element, or collect a `[]h.H` and spread it", byCompiler},
	{"`on.Debounce(\"250ms\")`, `on.Throttle(\"1s\")`", "a `time.Duration`: `on.Debounce(250*time.Millisecond)`", byCompiler},
	{"`on.Key(\"Enter\", fn)`", "`on.Keydown(fn)`, which has no key filter", byCompiler},
	{"`on.Indicator(sig)`, `on.Confirm`, `on.SetSignal`", "`h.DataIndicator(sig.Ref())`; `Confirm` and `SetSignal` are gone", byCompiler},
	{"`sig.Show()`, `sig.ShowUnless()`", "`h.DataShow(sig.Ref())`, `h.DataShow(sig.Ref().Not())`", byCompiler},
	{"`sig.Class(n)`, `sig.Style(p)`, `sig.Attr(n)`", "`h.DataClass(n, sig.Ref())`, `h.DataStyle(p, sig.Ref())`, `h.DataAttr(n, sig.Ref())`; `expr.Class` in a `CS` handler when no signal is needed", byCompiler},
	{"`h.DataShow(f, args…)`, `h.DataClass(n, f, args…)`, `h.DataOnClick(f, …)`", "an `expr.Expr`: `h.DataShow(e)`, `h.DataClass(n, e)`, `on.ClickCS(e)`", byCompiler},
	{"`via.Local(\"name\")`", "a `via.SignalCS[T]` field; `Toggle()` is `on.ClickCS(s.Ref().Toggle())`", byCompiler},
	{"`via.Computed(k, e)`, `via.Effect(e)`", "`h.DataComputed(k, e)`, `h.DataEffect(e)`", byCompiler},
	{"`h.Attr(n, v)`, `h.AttrNum(n, v)`", "`h.RawAttr(n, v)`", byCompiler},
	{"`h.Checked()`, `h.Disabled()`, `h.Required()`, `h.Selected()`", "a bool argument: `h.Checked(true)`", byCompiler},
	{"`h.ColSpan(\"2\")`, `h.TabIndex(\"0\")`, `h.MinNum(n)`, `h.ValueNum(n)`", "`h.ColSpan(2)`, `h.TabIndex(0)`, and the generic `h.Min(n)`, `h.Value(n)`", byCompiler},
	{"`h.Classes(…)`, `h.ClassMap(m)`, `h.Styles(…)`", "`h.Class(names…)` and `h.Style(css)`, with the names worked out in Go", byCompiler},
	{"`h.Tag`, `h.NewTag`, `h.VoidTag`", "`h.El(tag, …)`", byCompiler},
	{"`h.Raw(html)`, `h.Static`, `h.With`", "gone; there is no unescaped HTML node", byCompiler},
	{"`h.Title(s)`, the `<title>` element", "`PageMeta()` returning `via.Meta{Title: s}`; `h.Title` is now the `title` attribute", bySilent},
	{"`sess.Put(ctx, v)`, `sess.Get[T](ctx)`, `sess.Clear[T](ctx)`, `sess.Rotate(ctx)`", "`ctx.Session().Put(v)`, `.Get[T]()`, `.Delete()`, `.Rotate()`", byCompiler},
	{"one session value per type", "one value per session: a second `Put` replaces the first, so put one struct", bySilent},
	{"`StateSess[T]`", "a topic per `ctx.Session().ID()`, followed with `State.Track` in `OnInit`", byCompiler},
	{"`StateApp[T]`", "your own store, injected; a `topic.Topic[T]` and `via.StateTrack` to push its changes", byCompiler},
	{"`app.Broadcast`, `BroadcastSignals`, `BroadcastNotify`, `via.BroadcastSignal`", "`topic.New[T]()`, subscribed with `ctx.Listen` in `OnInit`", byCompiler},
	{"`OnConnect(ctx) error`", "`ctx.Tick` or `ctx.Listen` in `OnInit`; `ctx.OnConnect(fn)` for work on stream open", byWarn},
	{"`OnDispose(ctx)`", "`ctx.OnDispose(fn)`, registered in `OnInit`", bySilent},
	{"`via.Stream(ctx, d, fn)`", "`ctx.Tick(d, fn)` in `OnInit`", byCompiler},
	{"`ctx.Notify`, `ctx.ExecScript`, `ctx.Reload`, `ctx.SyncNow`, `ctx.Patch`", "gone", byCompiler},
	{"`ctx.Cookie`, `ctx.SetCookie`, `ctx.Writer()`", "`ctx.Request().Cookie(name)`; there is no response access, so keep the value in the session", byCompiler},
	{"`ctx.Done()`", "`ctx.Context().Done()`", byCompiler},
	{"`via.File` and `via.Files` fields, `ctx.MultipartReader()`", "`via.PostForm` and `ctx.Request().FormFile(name)`", byCompiler},
	{"`via.DecodeForm(ctx, &dst)`", "a bound `Signal` per field, read with `Get()`; or `via.PostForm` and `ctx.Request().FormValue`", byCompiler},
	{"`WithTitle`, `WithDescription`", "`PageMeta() via.Meta` on the mounted page", byCompiler},
	{"`WithLang`, `app.AppendToHead`, `app.AppendToFoot`", "`via.WithHead(via.Head{Lang, Raw, Assets})`; scripts and styles go in `Assets`", byCompiler},
	{"`WithPlugins(picocss.…)`", "your own CSS in `Head.Assets.Styles`", byCompiler},
	{"`WithPlugins(echarts.…)`, `maplibre`", "an island: `h.DataIgnoreMorph` and `h.DataEffect` around a script of yours", byCompiler},
	{"a Secure session cookie unless `WithInsecureCookies`", "Secure only when the request came over TLS; behind a TLS-terminating proxy set `WithSecureCookies`", bySilent},
	{"`app.Use`, `app.Group`, `app.Handle`, `app.HandleStatic`", "your own `http.ServeMux` and middleware around the `*via.Router`", byCompiler},
	{"`WithLogger(via.Logger)`, `WithMaxRequestBody`, `WithMaxUploadSize`", "`WithLogger(*slog.Logger)`, `WithMaxBody`, `WithMaxUpload`", byCompiler},
	{"`WithNotFound`", "`WithErrorPage`", byCompiler},
	{"`WithBackplane`, `StateAppEvents`, `vianats`", "gone; state lives in one process", byCompiler},
}

// Migrate is MIGRATION.md cut down to what a v0.7 app has to change.
type Migrate struct{ page }

func NewMigrate(env Env) Migrate { return Migrate{page: newPage("/migrate", env)} }

func (p *Migrate) PageMeta() via.Meta {
	return p.meta("Moving a v0.7 app to v0.8: the four shifts, a v0.7 to v0.8 mapping with what catches each change, and the wire breaks.")
}

func (p *Migrate) View() h.H {
	d := p.doc()
	plugins := append(inline("Plugins. picocss and its themes become your own CSS through `WithHead`; "+
		"echarts and maplibre become an "), h.A(h.Href(d.Href("/islands")), h.Str("island")), h.Str("."))
	return d.Page(
		h.P(h.Str("v0.8 is a rebuild. The module never left v0.x, so "), Code("go get -u"), h.Str(" moves v0.7 to v0.8 as an "+
			"ordinary bump and nothing warns you. Most v0.7 code does not port line by line, because the ideas "+
			"changed, not only the spellings. v0.7 is preserved on the v1 branch.")),

		d.H2("Short version"),
		h.Ul(
			h.Li(inline("Replace `h.Text` with `h.Str`.")...),
			h.Li(inline("Delete the `View(ctx)` parameter and load what the view reads in `OnInit`.")...),
			h.Li(inline("`StateTab` becomes `State`: drop `.Op(ctx)`, and `Read` and `Write` become `Get` and `Set`.")...),
			h.Li(join(inline("`via.New()` and `via.Mount[Page](app, …)` become "), APIText("via.NewRouter", "via.NewRouter()"),
				h.Str(" and "), APIText("via.Mount", "via.Mount(r, …, Page{})"), h.Str("."))...),
			h.Li(inline("Move `path:\"…\"` tags to `ctx.Param` in `OnInit`. Nothing flags a leftover tag.")...),
			h.Li(inline("Keep package `on`: `on.Click(p.Inc)` reads the same.")...),
		),
		h.P(h.Str("The compiler finds most of the rest. The changes it cannot see are listed after the mapping.")),

		d.H2("The four shifts"),
		d.H3("State is bare; ctx is for the request"),
		h.P(inline("Mutators take no ctx: `c.N.Set(c.N.Get() + 1)`. ctx is the request, used for sessions, path "+
			"params and subscriptions. `via.State[T]` is per tab and rendering one makes its unit live, so "+
			"the page streams. For a value every visitor shares, inject your own store and let the re-render read it.")...),
		d.H3("View is pure and takes no context"),
		h.P(inline("Anything a view needs is a field before `View` runs. `OnInit(*via.Ctx) error` loads it, on every "+
			"request. v0.7 logged an `OnInit` error and rendered anyway; v0.8 stops: `via.ErrNotFound` answers 404, "+
			"any other error 500. The hooks are duck-typed, so Mount panics on a hook name with the wrong signature "+
			"and warns on a near-miss name that has the right one.")...),
		d.H3("Composition is via.Child, and roots are taken by value"),
		h.P(join(inline("v0.7 rendered a child by calling its `View` by hand, `p.A.View(ctx, …)`, passing whatever it "+
			"needed. In v0.8 a child is a plain struct field rendered with "), APIText("via.Child", "via.Child(p.A)"),
			inline(", with its own `OnInit`; what used to be arguments are fields. "), API("via.Handler"), h.Str(" and "), API("via.Mount"),
			inline(" take the root by value, `Counter{…}` and not `&Counter{…}`. A live child may sit under plain ancestors; "+
				"a live child inside another live one panics at render."))...),
		d.H3("Fan-out is scoped to a topic"),
		h.P(join(inline("`app.Broadcast` and its siblings are gone. Publish on a "), APIText("topic.Topic", "topic.Topic[T]"),
			h.Str(" from anywhere and subscribe with "), APIText("via.Ctx.Listen", "ctx.Listen"),
			inline(" in `OnInit`; the subscription ends with the child. Nothing can push to a page that did not ask."))...),

		d.H3("The counter, both ways"),
		h.P(h.Str("v0.7, with per-tab state and the numeric shape:")),
		demo.Code(counterV07),
		h.P(inline("v0.8, the same counter as a `State[int]`. This is the live demo's source:")...),
		demo.Source("counter.go"),
		h.P(inline("Serve it with `http.ListenAndServe(\":3000\", via.Handler(Counter{}))`.")...),

		d.H2("Mapping"),
		h.P(inline("Ordered by how early a port hits each change. Caught by says what tells you: the compiler, "+
			"a panic or a warning when `Mount` walks the type at startup, or nothing.")...),
		table([]string{"v0.7", "v0.8", "Caught by"}, mappingRows()...),

		d.H2("What the compiler won't catch"),
		h.P(h.Str("These compile and start. The first sign is behaviour:")),
		h.Ul(silentRows()...),
		h.P(inline("A leftover `OnConnect(ctx) error` is warned about at `Mount` only when the type has no `OnInit`; "+
			"next to an `OnInit` it is dead code nothing reports.")...),

		d.H2("Removed outright"),
		h.Ul(
			h.Li(plugins...),
			h.Li(inline("`WithoutSSEReconnect`. The reconnect manager is always on.")...),
			h.Li(inline("The `sess` subpackage. Its functions are methods on `ctx.Session()`.")...),
			h.Li(inline("`StateSess`, `StateApp`, `StateAppEvents`, the numeric shapes and `.Op(ctx)`.")...),
			h.Li(inline("`Broadcast`, `BroadcastSignal`, `BroadcastSignals` and `BroadcastNotify`.")...),
			h.Li(inline("`via.File`, `via.Files`, `ctx.MultipartReader` and `via.DecodeForm`. `via.PostForm` is always "+
				"multipart; read a file with `ctx.Request().FormFile(name)`.")...),
			h.Li(inline("`app.Group` and its middleware. A page's access check moves into its `OnInit`.")...),
			h.Li(inline("`WithBackplane`, `vianats` and the key store. State lives in one process.")...),
			h.Li(inline("Most tuning options. `WithSSEHeartbeat` and `WithSSEWriteTimeout` are constants now; "+
				"`WithMaxSSEConn` and `WithPinnedDeadline` are the SSE options left.")...),
		),

		d.H2("Wire breaks"),
		h.P(h.Str("Nothing in your code builds these, so there is nothing to port. A tab left open across the "+
			"upgrade fails once and comes back correct on reload.")),
		h.Ul(
			h.Li(inline("Actions moved from `/_action/{id}` to `{path}/_via/a/{child}/{id}`, where id is a hash of "+
				"the handler's Go name. A stale tab's click answers 404.")...),
			h.Li(inline("The tab id signal is `viatab`, not `via_tab`.")...),
			h.Li(inline("A signal's wire name is its field path, first letter lower-cased: `count`, and "+
				"`chat__draft` inside an embedded `Chat`. The `via:\"name\"` override is gone.")...),
		),

		d.H2("New startup panics"),
		h.Ul(
			h.Li(inline("A `via` tag that is not `init=<json>`, or whose value is not JSON for the field's type.")...),
			h.Li(inline("A `Signal` that is not a plain field of its composition, reached through a pointer, "+
				"slice, array or map, or held by a type whose `View` has a value receiver, panics at `Mount` or `Child`.")...),
			h.Li(inline("Two fields that mint the same slot name panic: a nested `A.B` (`a_b`) next to a field `A_b`.")...),
			h.Li(inline("A `Mount` path with a `{name...}` or `{$}` wildcard, or one named `{child}` or `{act}`, "+
				"panics, and so does mounting both `/docs` and `/docs/`.")...),
		),

		d.H2("Names deprecated inside v0.8"),
		h.P(inline("Port straight to package `on` and `expr.Val`. `via.On` and `via.OnArg` still compile but are "+
			"deprecated in favour of `on.Click`, `on.Event` and `on.WithArg`, and `expr.Lit` is a deprecated alias "+
			"of `expr.Val`; all three are removed in v0.9. Package `on` and `expr.Val` are newer than the v0.8.1 "+
			"tag: on v0.8.1 itself, a click is `via.On(\"click\", p.Inc)`.")...),

		d.H2("Staying on v0.7"),
		h.P(h.Str("The v1 branch is preserved and its tags still resolve:")),
		demo.Plain("go get github.com/go-via/via@v0.7.0"),
		h.P(h.Str("v0.7 is frozen: no features, and no commitment to backport security fixes. Pinning it means "+
			"taking on its dependency maintenance yourself.")),

		h.P(h.Str("The full text, including the security defaults that moved and the rough edges in v0.8, is "),
			shell.ExtLink(repo+"MIGRATION.md", h.Str("MIGRATION.md")), h.Str(" on GitHub.")),
	)
}

func mappingRows() [][]h.H {
	rows := make([][]h.H, 0, len(changes))
	for _, c := range changes {
		rows = append(rows, []h.H{h.Span(inline(c.v07)...), h.Span(inline(c.v08)...), h.Str(c.caught)})
	}
	return rows
}

func silentRows() []h.H {
	var items []h.H
	for _, c := range changes {
		if c.caught != bySilent {
			continue
		}
		kids := append(inline(c.v07), h.Str(" → "))
		items = append(items, h.Li(append(kids, inline(c.v08)...)...))
	}
	return items
}

// join flattens inline runs and single nodes into one list of children.
func join(parts ...any) []h.H {
	var out []h.H
	for _, p := range parts {
		switch p := p.(type) {
		case []h.H:
			out = append(out, p...)
		case h.H:
			out = append(out, p)
		default:
			panic("join: not a node")
		}
	}
	return out
}

// inline renders a string whose backticked runs are code, the markup
// MIGRATION.md uses, so one row serves both.
func inline(s string) []h.H {
	parts := strings.Split(s, "`")
	out := make([]h.H, 0, len(parts))
	for i, t := range parts {
		switch {
		case t == "":
		case i%2 == 1:
			out = append(out, h.Code(h.Str(t)))
		default:
			out = append(out, h.Str(t))
		}
	}
	return out
}
