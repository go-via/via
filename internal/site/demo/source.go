package demo

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/go-via/via/h"
	"go-via.dev/site/demos"
)

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
		if cls := class(t.Type); cls != "" {
			spans = append(spans, h.Span(h.Class(cls), h.Str(t.Value)))
			continue
		}
		spans = append(spans, h.Str(t.Value))
	}
	return h.Pre(h.Class(ChromaPrefix+"chroma"), h.Code(spans...))
}

// class mirrors chroma's unexported html-formatter mapping.
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
	return ""
}
