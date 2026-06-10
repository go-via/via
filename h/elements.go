package h

// One constructor per HTML element. Each accepts a variadic of [H]
// values — attributes intermixed with content are reordered at render
// time. Void elements (no closing tag, content children dropped at
// render) are routed through [elVoid]. For ergonomic text construction
// see [T] / [Text].

// A renders the HTML <a> element.
func A(children ...H) H { return el("a", children) }

// Abbr renders the HTML <abbr> element.
func Abbr(children ...H) H { return el("abbr", children) }

// Address renders the HTML <address> element.
func Address(children ...H) H { return el("address", children) }

// Area renders the void HTML <area> element.
func Area(children ...H) H { return elVoid("area", children) }

// Article renders the HTML <article> element.
func Article(children ...H) H { return el("article", children) }

// Aside renders the HTML <aside> element.
func Aside(children ...H) H { return el("aside", children) }

// Audio renders the HTML <audio> element.
func Audio(children ...H) H { return el("audio", children) }

// B renders the HTML <b> element.
func B(children ...H) H { return el("b", children) }

// Base renders the void HTML <base> element.
func Base(children ...H) H { return elVoid("base", children) }

// BlockQuote renders the HTML <blockquote> element.
func BlockQuote(children ...H) H { return el("blockquote", children) }

// Body renders the HTML <body> element.
func Body(children ...H) H { return el("body", children) }

// Br renders the void HTML <br> element.
func Br(children ...H) H { return elVoid("br", children) }

// Button renders the HTML <button> element.
func Button(children ...H) H { return el("button", children) }

// Canvas renders the HTML <canvas> element.
func Canvas(children ...H) H { return el("canvas", children) }

// Caption renders the HTML <caption> element.
func Caption(children ...H) H { return el("caption", children) }

// Cite renders the HTML <cite> element.
func Cite(children ...H) H { return el("cite", children) }

// Code renders the HTML <code> element.
func Code(children ...H) H { return el("code", children) }

// Col renders the void HTML <col> element.
func Col(children ...H) H { return elVoid("col", children) }

// ColGroup renders the HTML <colgroup> element.
func ColGroup(children ...H) H { return el("colgroup", children) }

// DataList renders the HTML <datalist> element.
func DataList(children ...H) H { return el("datalist", children) }

// Dd renders the HTML <dd> element.
func Dd(children ...H) H { return el("dd", children) }

// Del renders the HTML <del> element.
func Del(children ...H) H { return el("del", children) }

// Details renders the HTML <details> element.
func Details(children ...H) H { return el("details", children) }

// Dfn renders the HTML <dfn> element.
func Dfn(children ...H) H { return el("dfn", children) }

// Dialog renders the HTML <dialog> element.
func Dialog(children ...H) H { return el("dialog", children) }

// Div renders the HTML <div> element.
func Div(children ...H) H { return el("div", children) }

// Dl renders the HTML <dl> element.
func Dl(children ...H) H { return el("dl", children) }

// Dt renders the HTML <dt> element.
func Dt(children ...H) H { return el("dt", children) }

// Em renders the HTML <em> element.
func Em(children ...H) H { return el("em", children) }

// Embed renders the void HTML <embed> element.
func Embed(children ...H) H { return elVoid("embed", children) }

// FieldSet renders the HTML <fieldset> element.
func FieldSet(children ...H) H { return el("fieldset", children) }

// FigCaption renders the HTML <figcaption> element.
func FigCaption(children ...H) H { return el("figcaption", children) }

// Figure renders the HTML <figure> element.
func Figure(children ...H) H { return el("figure", children) }

// Footer renders the HTML <footer> element.
func Footer(children ...H) H { return el("footer", children) }

// Form renders the HTML <form> element.
func Form(children ...H) H { return el("form", children) }

// H1 renders the HTML <h1> element.
func H1(children ...H) H { return el("h1", children) }

// H2 renders the HTML <h2> element.
func H2(children ...H) H { return el("h2", children) }

// H3 renders the HTML <h3> element.
func H3(children ...H) H { return el("h3", children) }

// H4 renders the HTML <h4> element.
func H4(children ...H) H { return el("h4", children) }

// H5 renders the HTML <h5> element.
func H5(children ...H) H { return el("h5", children) }

// H6 renders the HTML <h6> element.
func H6(children ...H) H { return el("h6", children) }

// Head renders the HTML <head> element.
func Head(children ...H) H { return el("head", children) }

// Header renders the HTML <header> element.
func Header(children ...H) H { return el("header", children) }

// Hr renders the void HTML <hr> element.
func Hr(children ...H) H { return elVoid("hr", children) }

// HGroup renders the HTML <hgroup> element.
func HGroup(children ...H) H { return el("hgroup", children) }

// HTML renders the HTML <html> element.
func HTML(children ...H) H { return el("html", children) }

// I renders the HTML <i> element.
func I(children ...H) H { return el("i", children) }

// IFrame renders the HTML <iframe> element.
func IFrame(children ...H) H { return el("iframe", children) }

// Img renders the void HTML <img> element.
func Img(children ...H) H { return elVoid("img", children) }

// Input renders the void HTML <input> element.
func Input(children ...H) H { return elVoid("input", children) }

// Ins renders the HTML <ins> element.
func Ins(children ...H) H { return el("ins", children) }

// Kbd renders the HTML <kbd> element.
func Kbd(children ...H) H { return el("kbd", children) }

// Label renders the HTML <label> element.
func Label(children ...H) H { return el("label", children) }

// Legend renders the HTML <legend> element.
func Legend(children ...H) H { return el("legend", children) }

// Li renders the HTML <li> element.
func Li(children ...H) H { return el("li", children) }

// Link renders the void HTML <link> element.
func Link(children ...H) H { return elVoid("link", children) }

// Main renders the HTML <main> element.
func Main(children ...H) H { return el("main", children) }

// Mark renders the HTML <mark> element.
func Mark(children ...H) H { return el("mark", children) }

// Meta renders the void HTML <meta> element.
func Meta(children ...H) H { return elVoid("meta", children) }

// Meter renders the HTML <meter> element.
func Meter(children ...H) H { return el("meter", children) }

// Nav renders the HTML <nav> element.
func Nav(children ...H) H { return el("nav", children) }

// NoScript renders the HTML <noscript> element.
func NoScript(children ...H) H { return el("noscript", children) }

// Object renders the HTML <object> element.
func Object(children ...H) H { return el("object", children) }

// Ol renders the HTML <ol> element.
func Ol(children ...H) H { return el("ol", children) }

// OptGroup renders the HTML <optgroup> element.
func OptGroup(children ...H) H { return el("optgroup", children) }

// Option renders the HTML <option> element.
func Option(children ...H) H { return el("option", children) }

// P renders the HTML <p> element.
func P(children ...H) H { return el("p", children) }

// Picture renders the HTML <picture> element.
func Picture(children ...H) H { return el("picture", children) }

// Pre renders the HTML <pre> element.
func Pre(children ...H) H { return el("pre", children) }

// Progress renders the HTML <progress> element.
func Progress(children ...H) H { return el("progress", children) }

// Q renders the HTML <q> element.
func Q(children ...H) H { return el("q", children) }

// S renders the HTML <s> element.
func S(children ...H) H { return el("s", children) }

// Samp renders the HTML <samp> element.
func Samp(children ...H) H { return el("samp", children) }

// Script renders the HTML <script> element.
func Script(children ...H) H { return el("script", children) }

// Section renders the HTML <section> element.
func Section(children ...H) H { return el("section", children) }

// Select renders the HTML <select> element.
func Select(children ...H) H { return el("select", children) }

// Small renders the HTML <small> element.
func Small(children ...H) H { return el("small", children) }

// Source renders the void HTML <source> element.
func Source(children ...H) H { return elVoid("source", children) }

// Span renders the HTML <span> element.
func Span(children ...H) H { return el("span", children) }

// Strong renders the HTML <strong> element.
func Strong(children ...H) H { return el("strong", children) }

// Sub renders the HTML <sub> element.
func Sub(children ...H) H { return el("sub", children) }

// Summary renders the HTML <summary> element.
func Summary(children ...H) H { return el("summary", children) }

// Sup renders the HTML <sup> element.
func Sup(children ...H) H { return el("sup", children) }

// Table renders the HTML <table> element.
func Table(children ...H) H { return el("table", children) }

// TBody renders the HTML <tbody> element.
func TBody(children ...H) H { return el("tbody", children) }

// Td renders the HTML <td> element.
func Td(children ...H) H { return el("td", children) }

// Template renders the HTML <template> element.
func Template(children ...H) H { return el("template", children) }

// Textarea renders the HTML <textarea> element.
func Textarea(children ...H) H { return el("textarea", children) }

// TFoot renders the HTML <tfoot> element.
func TFoot(children ...H) H { return el("tfoot", children) }

// Th renders the HTML <th> element.
func Th(children ...H) H { return el("th", children) }

// THead renders the HTML <thead> element.
func THead(children ...H) H { return el("thead", children) }

// Time renders the HTML <time> element.
func Time(children ...H) H { return el("time", children) }

// Tr renders the HTML <tr> element.
func Tr(children ...H) H { return el("tr", children) }

// U renders the HTML <u> element.
func U(children ...H) H { return el("u", children) }

// Ul renders the HTML <ul> element.
func Ul(children ...H) H { return el("ul", children) }

// Var renders the HTML <var> element.
func Var(children ...H) H { return el("var", children) }

// StyleEl renders the HTML <style> element.
func StyleEl(children ...H) H { return el("style", children) }

// Video renders the HTML <video> element.
func Video(children ...H) H { return el("video", children) }

// Wbr renders the void HTML <wbr> element.
func Wbr(children ...H) H { return elVoid("wbr", children) }

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
