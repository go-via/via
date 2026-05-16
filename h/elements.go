package h

// One constructor per HTML element. Each accepts a variadic of [H]
// values — attributes intermixed with content are reordered at render
// time. Void elements (no closing tag, content children dropped at
// render) are routed through [elVoid]. For ergonomic text construction
// see [S] / [Text].

func A(children ...H) H          { return el("a", children) }
func Abbr(children ...H) H       { return el("abbr", children) }
func Address(children ...H) H    { return el("address", children) }
func Area(children ...H) H       { return elVoid("area", children) }
func Article(children ...H) H    { return el("article", children) }
func Aside(children ...H) H      { return el("aside", children) }
func Audio(children ...H) H      { return el("audio", children) }
func B(children ...H) H          { return el("b", children) }
func Base(children ...H) H       { return elVoid("base", children) }
func BlockQuote(children ...H) H { return el("blockquote", children) }
func Body(children ...H) H       { return el("body", children) }
func Br(children ...H) H         { return elVoid("br", children) }
func Button(children ...H) H     { return el("button", children) }
func Canvas(children ...H) H     { return el("canvas", children) }
func Caption(children ...H) H    { return el("caption", children) }
func Cite(children ...H) H       { return el("cite", children) }
func Code(children ...H) H       { return el("code", children) }
func Col(children ...H) H        { return elVoid("col", children) }
func ColGroup(children ...H) H   { return el("colgroup", children) }
func DataList(children ...H) H   { return el("datalist", children) }
func Dd(children ...H) H         { return el("dd", children) }
func Del(children ...H) H        { return el("del", children) }
func Details(children ...H) H    { return el("details", children) }
func Dfn(children ...H) H        { return el("dfn", children) }
func Dialog(children ...H) H     { return el("dialog", children) }
func Div(children ...H) H        { return el("div", children) }
func Dl(children ...H) H         { return el("dl", children) }
func Dt(children ...H) H         { return el("dt", children) }
func Em(children ...H) H         { return el("em", children) }
func Embed(children ...H) H      { return elVoid("embed", children) }
func FieldSet(children ...H) H   { return el("fieldset", children) }
func FigCaption(children ...H) H { return el("figcaption", children) }
func Figure(children ...H) H     { return el("figure", children) }
func Footer(children ...H) H     { return el("footer", children) }
func Form(children ...H) H       { return el("form", children) }
func H1(children ...H) H         { return el("h1", children) }
func H2(children ...H) H         { return el("h2", children) }
func H3(children ...H) H         { return el("h3", children) }
func H4(children ...H) H         { return el("h4", children) }
func H5(children ...H) H         { return el("h5", children) }
func H6(children ...H) H         { return el("h6", children) }
func Head(children ...H) H       { return el("head", children) }
func Header(children ...H) H     { return el("header", children) }
func Hr(children ...H) H         { return elVoid("hr", children) }
func HGroup(children ...H) H     { return el("hgroup", children) }
func HTML(children ...H) H       { return el("html", children) }
func I(children ...H) H          { return el("i", children) }
func IFrame(children ...H) H     { return el("iframe", children) }
func Img(children ...H) H        { return elVoid("img", children) }
func Input(children ...H) H      { return elVoid("input", children) }
func Ins(children ...H) H        { return el("ins", children) }
func Kbd(children ...H) H        { return el("kbd", children) }
func Label(children ...H) H      { return el("label", children) }
func Legend(children ...H) H     { return el("legend", children) }
func Li(children ...H) H         { return el("li", children) }
func Link(children ...H) H       { return elVoid("link", children) }
func Main(children ...H) H       { return el("main", children) }
func Mark(children ...H) H       { return el("mark", children) }
func Meta(children ...H) H       { return elVoid("meta", children) }
func Meter(children ...H) H      { return el("meter", children) }
func Nav(children ...H) H        { return el("nav", children) }
func NoScript(children ...H) H   { return el("noscript", children) }
func Object(children ...H) H     { return el("object", children) }
func Ol(children ...H) H         { return el("ol", children) }
func OptGroup(children ...H) H   { return el("optgroup", children) }
func Option(children ...H) H     { return el("option", children) }
func P(children ...H) H          { return el("p", children) }
func Picture(children ...H) H    { return el("picture", children) }
func Pre(children ...H) H        { return el("pre", children) }
func Progress(children ...H) H   { return el("progress", children) }
func Q(children ...H) H          { return el("q", children) }
func S(children ...H) H          { return el("s", children) }
func Samp(children ...H) H       { return el("samp", children) }
func Script(children ...H) H     { return el("script", children) }
func Section(children ...H) H    { return el("section", children) }
func Select(children ...H) H     { return el("select", children) }
func Small(children ...H) H      { return el("small", children) }
func Source(children ...H) H     { return elVoid("source", children) }
func Span(children ...H) H       { return el("span", children) }
func Strong(children ...H) H     { return el("strong", children) }
func Sub(children ...H) H        { return el("sub", children) }
func Summary(children ...H) H    { return el("summary", children) }
func Sup(children ...H) H        { return el("sup", children) }
func Table(children ...H) H      { return el("table", children) }
func TBody(children ...H) H      { return el("tbody", children) }
func Td(children ...H) H         { return el("td", children) }
func Template(children ...H) H   { return el("template", children) }
func Textarea(children ...H) H   { return el("textarea", children) }
func TFoot(children ...H) H      { return el("tfoot", children) }
func Th(children ...H) H         { return el("th", children) }
func THead(children ...H) H      { return el("thead", children) }
func Time(children ...H) H       { return el("time", children) }
func Tr(children ...H) H         { return el("tr", children) }
func U(children ...H) H          { return el("u", children) }
func Ul(children ...H) H         { return el("ul", children) }
func Var(children ...H) H        { return el("var", children) }
func StyleEl(children ...H) H    { return el("style", children) }
func Video(children ...H) H      { return el("video", children) }
func Wbr(children ...H) H        { return elVoid("wbr", children) }

// Title emits <title>v</title> with v HTML-escaped. Defined alongside
// element constructors because it produces an element node, not an
// attribute.
func Title(v string) H { return el("title", []H{Text(v)}) }

// Tag emits a custom non-void element. Use it for tags absent from
// the static constructor list (web components, SVG primitives, etc.).
// The tag name is written verbatim — callers must supply a valid HTML
// element name; nothing here validates it.
func Tag(name string, children ...H) H { return el(name, children) }

// VoidTag emits a custom void element (no closing tag, content
// children dropped at render time). The tag name is written verbatim
// — callers must supply a valid HTML element name; nothing here
// validates it.
func VoidTag(name string, children ...H) H { return elVoid(name, children) }

// NewTag returns a reusable constructor for the given tag name. Use
// it when a custom element should share the call shape of the
// built-in constructors:
//
//	var SVG = h.NewTag("svg")
//	SVG(h.Attr("xmlns", "http://www.w3.org/2000/svg"), shapes...)
func NewTag(name string) func(children ...H) H {
	return func(children ...H) H { return el(name, children) }
}

// NewVoidTag is [NewTag] for void elements.
func NewVoidTag(name string) func(children ...H) H {
	return func(children ...H) H { return elVoid(name, children) }
}
