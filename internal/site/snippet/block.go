package snippet

import (
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"go-via.dev/site/icon"
)

// The highlighting contract between this package and snippet/gen: change
// either and static/chroma.css is stale until the generator has run.
//
//go:generate go run ./gen
const (
	ChromaStyle  = "via-onedark"
	ChromaPrefix = "c-"
)

// Highlight is an unlabelled Go block of src. Hand-typed Go is not
// compile-checked: it exists for callers not yet moved to Show and Region.
func Highlight(src string) h.H { return frame("", goPre(src, nil, false)) }

// Text is a plain block for shell commands and config files, labelled like a
// Go one ("Caddyfile", "/etc/init.d/go-via"). An empty label leaves the header
// out.
func Text(label, text string) h.H {
	return frame(label, h.Pre(h.Code(h.Str(text))))
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

// goPre highlights with classes only; style-src 'self' blocks inline styles.
// Only a block with marked lines wraps each line, so an unmarked one is the
// flat token run chroma's own formatter writes.
func goPre(src string, hl []bool, lined bool) h.H {
	it, err := lexers.Get("go").Tokenise(nil, src)
	if err != nil {
		return h.Pre(h.Class(ChromaPrefix+"chroma"), h.Code(h.Str(src)))
	}
	if !lined {
		var spans []h.H
		for _, t := range it.Tokens() {
			spans = append(spans, token(t.Type, t.Value))
		}
		return h.Pre(h.Class(ChromaPrefix+"chroma"), h.Code(spans...))
	}
	lines := [][]h.H{nil}
	for _, t := range it.Tokens() {
		for i, part := range strings.Split(t.Value, "\n") {
			if i > 0 {
				lines = append(lines, nil)
			}
			if part != "" {
				lines[len(lines)-1] = append(lines[len(lines)-1], token(t.Type, part))
			}
		}
	}
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	rows := make([]h.H, 0, len(lines))
	for i, parts := range lines {
		mark := ""
		if i < len(hl) && hl[i] {
			mark = "hl"
		}
		rows = append(rows, h.Span(append([]h.H{h.Class("ln", mark)}, append(parts, h.Str("\n"))...)...))
	}
	return h.Pre(h.Class(ChromaPrefix+"chroma", "code-hl"), h.Code(rows...))
}

func token(t chroma.TokenType, v string) h.H {
	if cls := Class(t); cls != "" {
		return h.Span(h.Class(cls), h.Str(v))
	}
	return h.Str(v)
}

// frame puts the label and a copy button on a block. The text is read off the
// DOM rather than passed as expr.Val: Datastar rewrites "@name(" even inside
// string literals, so a sample containing @post( would be corrupted, and a
// literal would repeat every block's text in an attribute.
func frame(label string, pre h.H) h.H {
	var head h.H
	labelled := ""
	if label != "" {
		head, labelled = h.Div(h.Class("code-name"), h.Str(label)), "code-file"
	}
	return h.Div(h.Class("code", labelled),
		head,
		pre,
		h.Button(h.Class("copy"), h.Type("button"), h.Aria("label", "Copy code"),
			on.ClickCS(expr.Do(expr.CopyTextOf("pre"), expr.Class("copied", true))),
			on.ClickCS(expr.Class("copied", false), on.Debounce(1500*time.Millisecond)),
			icon.Clipboard(), icon.Check(),
			h.Span(h.Class("copy-done"), h.Aria("live", "polite"), h.Str("Copied"))),
	)
}
