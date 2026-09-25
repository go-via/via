package demo

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/go-via/via/h"
	"go-via.dev/site/demos"
)

// The highlighting contract between this package and demo/gen: change either
// and static/chroma.css is stale until the generator has run.
//
//go:generate go run ./gen
const (
	ChromaStyle  = "github-dark"
	ChromaPrefix = "c-"
)

// h nodes are immutable, so one highlighted tree serves every render.
var sources = map[string]h.H{}

func init() {
	entries, err := demos.FS.ReadDir(".")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		src, err := demos.FS.ReadFile(e.Name())
		if err != nil {
			panic(err)
		}
		sources[e.Name()] = Code(string(src))
	}
}

// Source is the highlighted text of one demos file. An unembedded name is a
// typo in a demo.Card call site, so it panics rather than render a blank tab.
func Source(name string) h.H {
	src, ok := sources[name]
	if !ok {
		panic("demo: no embedded source named " + name)
	}
	return src
}

// Code highlights with classes only; style-src 'self' blocks inline styles.
func Code(src string) h.H {
	it, err := lexers.Get("go").Tokenise(nil, src)
	if err != nil {
		return h.Pre(h.Class(ChromaPrefix+"chroma"), h.Code(h.Str(src)))
	}
	var spans []h.H
	for _, t := range it.Tokens() {
		if cls := Class(t.Type); cls != "" {
			spans = append(spans, h.Span(h.Class(cls), h.Str(t.Value)))
			continue
		}
		spans = append(spans, h.Str(t.Value))
	}
	return h.Pre(h.Class(ChromaPrefix+"chroma"), h.Code(spans...))
}

// Class mirrors chroma's unexported html-formatter mapping: the prefixed CSS
// class a token gets, or "" for a token that gets no span at all.
func Class(t chroma.TokenType) string {
	for t != 0 {
		cls, ok := chroma.StandardTypes[t]
		if ok {
			if cls == "" {
				return ""
			}
			return ChromaPrefix + cls
		}
		t = t.Parent()
	}
	return ""
}
