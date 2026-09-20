// Package demo renders a documentation demo: the live child, its verbatim
// source, and a pane the inspector fills.
package demo

import (
	"log"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/go-via/via/h"
	"go-via.dev/site/demos"
)

const (
	// ChromaStyle and ChromaPrefix must match the committed static/chroma.css;
	// demo/gen writes that file from these two.
	ChromaStyle  = "github-dark"
	ChromaPrefix = "c-"
)

// sources holds every embedded demo file already turned into spans. The h
// nodes are immutable, so one tree serves every concurrent render.
var sources = map[string]h.H{}

func init() {
	entries, err := demos.FS.ReadDir(".")
	if err != nil {
		log.Fatal(err)
	}
	for _, e := range entries {
		src, err := demos.FS.ReadFile(e.Name())
		if err != nil {
			log.Fatal(err)
		}
		sources[e.Name()] = Code(string(src))
	}
}

// Source is the highlighted text of a file in the demos package, highlighted
// once at startup. An unknown name is a typo in a call site and panics.
func Source(name string) h.H {
	src, ok := sources[name]
	if !ok {
		panic("demo: no embedded source named " + name)
	}
	return src
}

// Code highlights an arbitrary Go snippet. Highlighting is CSS classes only —
// an inline style attribute would be blocked by the site's style-src 'self'.
func Code(src string) h.H {
	it, err := lexers.Get("go").Tokenise(nil, src)
	if err != nil {
		return h.Pre(h.Class(ChromaPrefix+"chroma"), h.Code(h.Str(src)))
	}
	var spans []h.H
	for _, t := range it.Tokens() {
		if cls := class(t.Type); cls != "" {
			spans = append(spans, h.Span(h.Class(cls), h.Str(t.Value)))
			continue
		}
		spans = append(spans, h.Str(t.Value))
	}
	return h.Pre(h.Class(ChromaPrefix+"chroma"), h.Code(spans...))
}

// class mirrors chroma's html formatter, which keeps its own mapping
// unexported: walk to the nearest ancestor token type that has a class, so the
// spans here and the rules demo/gen writes name the same thing.
func class(t chroma.TokenType) string {
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
	if cls := chroma.StandardTypes[t]; cls != "" {
		return ChromaPrefix + cls
	}
	return ""
}
