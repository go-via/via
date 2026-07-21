package h

import "github.com/go-via/via/internal/hcore"

// The full HTML5 element vocabulary, one constructor per tag, minus the tags a
// via View has no business emitting: html, head, body, script, style, title,
// base, meta, link, template, slot and data are via's (the page shell, CSP'd
// scripts) or footguns (template/slot collide with composition, data with
// Datastar). Exotic or future tags go through El("tag", …).
//
// Mechanically uniform on purpose — every constructor is element{tag, kids} —
// so the whole file reads as a table, not code.

func A(kids ...H) H          { return hcore.El("a", kids...) }
func Abbr(kids ...H) H       { return hcore.El("abbr", kids...) }
func Address(kids ...H) H    { return hcore.El("address", kids...) }
func Area(kids ...H) H       { return hcore.El("area", kids...) }
func Article(kids ...H) H    { return hcore.El("article", kids...) }
func Aside(kids ...H) H      { return hcore.El("aside", kids...) }
func Audio(kids ...H) H      { return hcore.El("audio", kids...) }
func B(kids ...H) H          { return hcore.El("b", kids...) }
func Bdi(kids ...H) H        { return hcore.El("bdi", kids...) }
func Bdo(kids ...H) H        { return hcore.El("bdo", kids...) }
func Blockquote(kids ...H) H { return hcore.El("blockquote", kids...) }
func Br(kids ...H) H         { return hcore.El("br", kids...) }
func Button(kids ...H) H     { return hcore.El("button", kids...) }
func Canvas(kids ...H) H     { return hcore.El("canvas", kids...) }
func Caption(kids ...H) H    { return hcore.El("caption", kids...) }
func Cite(kids ...H) H       { return hcore.El("cite", kids...) }
func Code(kids ...H) H       { return hcore.El("code", kids...) }
func Col(kids ...H) H        { return hcore.El("col", kids...) }
func Colgroup(kids ...H) H   { return hcore.El("colgroup", kids...) }
func Datalist(kids ...H) H   { return hcore.El("datalist", kids...) }
func Dd(kids ...H) H         { return hcore.El("dd", kids...) }
func Del(kids ...H) H        { return hcore.El("del", kids...) }
func Details(kids ...H) H    { return hcore.El("details", kids...) }
func Dfn(kids ...H) H        { return hcore.El("dfn", kids...) }
func Dialog(kids ...H) H     { return hcore.El("dialog", kids...) }
func Div(kids ...H) H        { return hcore.El("div", kids...) }
func Dl(kids ...H) H         { return hcore.El("dl", kids...) }
func Dt(kids ...H) H         { return hcore.El("dt", kids...) }
func Em(kids ...H) H         { return hcore.El("em", kids...) }
func Embed(kids ...H) H      { return hcore.El("embed", kids...) }
func Fieldset(kids ...H) H   { return hcore.El("fieldset", kids...) }
func Figcaption(kids ...H) H { return hcore.El("figcaption", kids...) }
func Figure(kids ...H) H     { return hcore.El("figure", kids...) }
func Footer(kids ...H) H     { return hcore.El("footer", kids...) }
func Form(kids ...H) H       { return hcore.El("form", kids...) }
func H1(kids ...H) H         { return hcore.El("h1", kids...) }
func H2(kids ...H) H         { return hcore.El("h2", kids...) }
func H3(kids ...H) H         { return hcore.El("h3", kids...) }
func H4(kids ...H) H         { return hcore.El("h4", kids...) }
func H5(kids ...H) H         { return hcore.El("h5", kids...) }
func H6(kids ...H) H         { return hcore.El("h6", kids...) }
func Header(kids ...H) H     { return hcore.El("header", kids...) }
func Hgroup(kids ...H) H     { return hcore.El("hgroup", kids...) }
func Hr(kids ...H) H         { return hcore.El("hr", kids...) }
func I(kids ...H) H          { return hcore.El("i", kids...) }
func Iframe(kids ...H) H     { return hcore.El("iframe", kids...) }
func Img(kids ...H) H        { return hcore.El("img", kids...) }
func Input(kids ...H) H      { return hcore.El("input", kids...) }
func Ins(kids ...H) H        { return hcore.El("ins", kids...) }
func Kbd(kids ...H) H        { return hcore.El("kbd", kids...) }
func Label(kids ...H) H      { return hcore.El("label", kids...) }
func Legend(kids ...H) H     { return hcore.El("legend", kids...) }
func Li(kids ...H) H         { return hcore.El("li", kids...) }
func Main(kids ...H) H       { return hcore.El("main", kids...) }
func Map(kids ...H) H        { return hcore.El("map", kids...) }
func Mark(kids ...H) H       { return hcore.El("mark", kids...) }
func Menu(kids ...H) H       { return hcore.El("menu", kids...) }
func Meter(kids ...H) H      { return hcore.El("meter", kids...) }
func Nav(kids ...H) H        { return hcore.El("nav", kids...) }
func Noscript(kids ...H) H   { return hcore.El("noscript", kids...) }
func Object(kids ...H) H     { return hcore.El("object", kids...) }
func Ol(kids ...H) H         { return hcore.El("ol", kids...) }
func Optgroup(kids ...H) H   { return hcore.El("optgroup", kids...) }
func Option(kids ...H) H     { return hcore.El("option", kids...) }
func Output(kids ...H) H     { return hcore.El("output", kids...) }
func P(kids ...H) H          { return hcore.El("p", kids...) }
func Picture(kids ...H) H    { return hcore.El("picture", kids...) }
func Pre(kids ...H) H        { return hcore.El("pre", kids...) }
func Progress(kids ...H) H   { return hcore.El("progress", kids...) }
func Q(kids ...H) H          { return hcore.El("q", kids...) }
func Rp(kids ...H) H         { return hcore.El("rp", kids...) }
func Rt(kids ...H) H         { return hcore.El("rt", kids...) }
func Ruby(kids ...H) H       { return hcore.El("ruby", kids...) }
func S(kids ...H) H          { return hcore.El("s", kids...) }
func Samp(kids ...H) H       { return hcore.El("samp", kids...) }
func Search(kids ...H) H     { return hcore.El("search", kids...) }
func Section(kids ...H) H    { return hcore.El("section", kids...) }
func Select(kids ...H) H     { return hcore.El("select", kids...) }
func Small(kids ...H) H      { return hcore.El("small", kids...) }
func Source(kids ...H) H     { return hcore.El("source", kids...) }
func Span(kids ...H) H       { return hcore.El("span", kids...) }
func Strong(kids ...H) H     { return hcore.El("strong", kids...) }
func Sub(kids ...H) H        { return hcore.El("sub", kids...) }
func Summary(kids ...H) H    { return hcore.El("summary", kids...) }
func Sup(kids ...H) H        { return hcore.El("sup", kids...) }
func Table(kids ...H) H      { return hcore.El("table", kids...) }
func Tbody(kids ...H) H      { return hcore.El("tbody", kids...) }
func Td(kids ...H) H         { return hcore.El("td", kids...) }
func Textarea(kids ...H) H   { return hcore.El("textarea", kids...) }
func Tfoot(kids ...H) H      { return hcore.El("tfoot", kids...) }
func Th(kids ...H) H         { return hcore.El("th", kids...) }
func Thead(kids ...H) H      { return hcore.El("thead", kids...) }
func Time(kids ...H) H       { return hcore.El("time", kids...) }
func Tr(kids ...H) H         { return hcore.El("tr", kids...) }
func Track(kids ...H) H      { return hcore.El("track", kids...) }
func U(kids ...H) H          { return hcore.El("u", kids...) }
func Ul(kids ...H) H         { return hcore.El("ul", kids...) }
func Var(kids ...H) H        { return hcore.El("var", kids...) }
func Video(kids ...H) H      { return hcore.El("video", kids...) }
func Wbr(kids ...H) H        { return hcore.El("wbr", kids...) }
