package h

import "github.com/go-via/via/internal/render"

// The full HTML5 element vocabulary, one constructor per tag, minus the tags a
// via View has no business emitting: html, head, body, script, style, title,
// base, meta, link, template, slot and data are via's (the page shell, CSP'd
// scripts) or footguns (template/slot collide with composition, data with
// Datastar). Exotic or future tags go through El("tag", …).
//
// Mechanically uniform on purpose — every constructor is render.El(tag, kids…)
// — so the whole file reads as a table, not code.

func A(kids ...H) H          { return render.El("a", kids...) }
func Abbr(kids ...H) H       { return render.El("abbr", kids...) }
func Address(kids ...H) H    { return render.El("address", kids...) }
func Area(kids ...H) H       { return render.El("area", kids...) }
func Article(kids ...H) H    { return render.El("article", kids...) }
func Aside(kids ...H) H      { return render.El("aside", kids...) }
func Audio(kids ...H) H      { return render.El("audio", kids...) }
func B(kids ...H) H          { return render.El("b", kids...) }
func Bdi(kids ...H) H        { return render.El("bdi", kids...) }
func Bdo(kids ...H) H        { return render.El("bdo", kids...) }
func Blockquote(kids ...H) H { return render.El("blockquote", kids...) }
func Br(kids ...H) H         { return render.El("br", kids...) }
func Button(kids ...H) H     { return render.El("button", kids...) }
func Canvas(kids ...H) H     { return render.El("canvas", kids...) }
func Caption(kids ...H) H    { return render.El("caption", kids...) }
func Cite(kids ...H) H       { return render.El("cite", kids...) }
func Code(kids ...H) H       { return render.El("code", kids...) }
func Col(kids ...H) H        { return render.El("col", kids...) }
func Colgroup(kids ...H) H   { return render.El("colgroup", kids...) }
func Datalist(kids ...H) H   { return render.El("datalist", kids...) }
func Dd(kids ...H) H         { return render.El("dd", kids...) }
func Del(kids ...H) H        { return render.El("del", kids...) }
func Details(kids ...H) H    { return render.El("details", kids...) }
func Dfn(kids ...H) H        { return render.El("dfn", kids...) }
func Dialog(kids ...H) H     { return render.El("dialog", kids...) }
func Div(kids ...H) H        { return render.El("div", kids...) }
func Dl(kids ...H) H         { return render.El("dl", kids...) }
func Dt(kids ...H) H         { return render.El("dt", kids...) }
func Em(kids ...H) H         { return render.El("em", kids...) }
func Embed(kids ...H) H      { return render.El("embed", kids...) }
func Fieldset(kids ...H) H   { return render.El("fieldset", kids...) }
func Figcaption(kids ...H) H { return render.El("figcaption", kids...) }
func Figure(kids ...H) H     { return render.El("figure", kids...) }
func Footer(kids ...H) H     { return render.El("footer", kids...) }
func Form(kids ...H) H       { return render.El("form", kids...) }
func H1(kids ...H) H         { return render.El("h1", kids...) }
func H2(kids ...H) H         { return render.El("h2", kids...) }
func H3(kids ...H) H         { return render.El("h3", kids...) }
func H4(kids ...H) H         { return render.El("h4", kids...) }
func H5(kids ...H) H         { return render.El("h5", kids...) }
func H6(kids ...H) H         { return render.El("h6", kids...) }
func Header(kids ...H) H     { return render.El("header", kids...) }
func Hgroup(kids ...H) H     { return render.El("hgroup", kids...) }
func Hr(kids ...H) H         { return render.El("hr", kids...) }
func I(kids ...H) H          { return render.El("i", kids...) }
func Iframe(kids ...H) H     { return render.El("iframe", kids...) }
func Img(kids ...H) H        { return render.El("img", kids...) }
func Input(kids ...H) H      { return render.El("input", kids...) }
func Ins(kids ...H) H        { return render.El("ins", kids...) }
func Kbd(kids ...H) H        { return render.El("kbd", kids...) }
func Label(kids ...H) H      { return render.El("label", kids...) }
func Legend(kids ...H) H     { return render.El("legend", kids...) }
func Li(kids ...H) H         { return render.El("li", kids...) }
func Main(kids ...H) H       { return render.El("main", kids...) }
func Map(kids ...H) H        { return render.El("map", kids...) }
func Mark(kids ...H) H       { return render.El("mark", kids...) }
func Menu(kids ...H) H       { return render.El("menu", kids...) }
func Meter(kids ...H) H      { return render.El("meter", kids...) }
func Nav(kids ...H) H        { return render.El("nav", kids...) }
func Noscript(kids ...H) H   { return render.El("noscript", kids...) }
func Object(kids ...H) H     { return render.El("object", kids...) }
func Ol(kids ...H) H         { return render.El("ol", kids...) }
func Optgroup(kids ...H) H   { return render.El("optgroup", kids...) }
func Option(kids ...H) H     { return render.El("option", kids...) }
func Output(kids ...H) H     { return render.El("output", kids...) }
func P(kids ...H) H          { return render.El("p", kids...) }
func Picture(kids ...H) H    { return render.El("picture", kids...) }
func Pre(kids ...H) H        { return render.El("pre", kids...) }
func Progress(kids ...H) H   { return render.El("progress", kids...) }
func Q(kids ...H) H          { return render.El("q", kids...) }
func Rp(kids ...H) H         { return render.El("rp", kids...) }
func Rt(kids ...H) H         { return render.El("rt", kids...) }
func Ruby(kids ...H) H       { return render.El("ruby", kids...) }
func S(kids ...H) H          { return render.El("s", kids...) }
func Samp(kids ...H) H       { return render.El("samp", kids...) }
func Search(kids ...H) H     { return render.El("search", kids...) }
func Section(kids ...H) H    { return render.El("section", kids...) }
func Select(kids ...H) H     { return render.El("select", kids...) }
func Small(kids ...H) H      { return render.El("small", kids...) }
func Source(kids ...H) H     { return render.El("source", kids...) }
func Span(kids ...H) H       { return render.El("span", kids...) }
func Strong(kids ...H) H     { return render.El("strong", kids...) }
func Sub(kids ...H) H        { return render.El("sub", kids...) }
func Summary(kids ...H) H    { return render.El("summary", kids...) }
func Sup(kids ...H) H        { return render.El("sup", kids...) }
func Table(kids ...H) H      { return render.El("table", kids...) }
func Tbody(kids ...H) H      { return render.El("tbody", kids...) }
func Td(kids ...H) H         { return render.El("td", kids...) }
func Textarea(kids ...H) H   { return render.El("textarea", kids...) }
func Tfoot(kids ...H) H      { return render.El("tfoot", kids...) }
func Th(kids ...H) H         { return render.El("th", kids...) }
func Thead(kids ...H) H      { return render.El("thead", kids...) }
func Time(kids ...H) H       { return render.El("time", kids...) }
func Tr(kids ...H) H         { return render.El("tr", kids...) }
func Track(kids ...H) H      { return render.El("track", kids...) }
func U(kids ...H) H          { return render.El("u", kids...) }
func Ul(kids ...H) H         { return render.El("ul", kids...) }
func Var(kids ...H) H        { return render.El("var", kids...) }
func Video(kids ...H) H      { return render.El("video", kids...) }
func Wbr(kids ...H) H        { return render.El("wbr", kids...) }
