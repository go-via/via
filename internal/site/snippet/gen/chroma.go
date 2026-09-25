package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"go-via.dev/site/snippet"
)

// pageBG matches site.css's <pre>; the style's own dark shows as a seam.
const pageBG = "#1b1e24"

// The v0.7 docs' one-dark palette. chroma's own onedark colours every
// identifier red; v0.7 leaves names in the text colour, and its comments are
// italic.
var _ = styles.Register(chroma.MustNewStyle(snippet.ChromaStyle, chroma.StyleEntries{
	chroma.Background:          "#abb2bf bg:#282c34",
	chroma.Keyword:             "#c678dd",
	chroma.KeywordConstant:     "#d19a66",
	chroma.NameBuiltin:         "#e5c07b",
	chroma.LiteralString:       "#98c379",
	chroma.LiteralStringEscape: "#56b6c2",
	chroma.LiteralNumber:       "#d19a66",
	chroma.Comment:             "italic #7f848e",
}))

// chromaSample covers token kinds a snippet.Highlight caller may use that no
// embedded file does.
const chromaSample = `// comment
/* block */
package p

import "fmt"

type T struct{ F float64 }

const (
	A    = 0x1f
	B    = 1.5e3
	C    = 'x'
	D    = "\n"
	Back = ` + "`raw`" + `
)

func (t *T) M(xs ...int) (int, error) {
loop:
	for i, x := range xs {
		switch {
		case x&1 == 0:
			continue loop
		default:
			go func() { fmt.Println(i, x, true, nil) }()
		}
	}
	return len(xs) % 3, nil
}
`

// chromaCSS is static/chroma.css: rules only for classes Go source can emit.
// chroma's WriteCSS would also write line-number and line-link rules.
func chromaCSS() string {
	style := styles.Get(snippet.ChromaStyle)
	if style == nil {
		panic("gen: no chroma style " + snippet.ChromaStyle)
	}

	used := map[string]bool{}
	for _, src := range append(snippet.Sources(), chromaSample) {
		it, err := lexers.Get("go").Tokenise(nil, src)
		if err != nil {
			panic(err)
		}
		for _, t := range it.Tokens() {
			if cls := snippet.Class(t.Type); cls != "" {
				used[cls] = true
			}
		}
	}

	bg := style.Get(chroma.Background)
	var b strings.Builder
	// No .bg rule: chroma's formatter writes one for a wrapper snippet never
	// renders, and bg is only here to be subtracted from each token's style.
	fmt.Fprintf(&b, "/* PreWrapper */ .%schroma { color: %s; background-color: %s }\n", snippet.ChromaPrefix, bg.Colour, pageBG)

	types := make([]int, 0, len(chroma.StandardTypes))
	for t := range chroma.StandardTypes {
		types = append(types, int(t))
	}
	slices.Sort(types)
	for _, i := range types {
		t := chroma.TokenType(i)
		cls := chroma.StandardTypes[t]
		if cls == "" || !used[snippet.ChromaPrefix+cls] {
			continue
		}
		// Sub(bg): as chroma's formatter, don't repeat what the background carries.
		css := html.StyleEntryToCSS(style.Get(t).Sub(bg))
		if css == "" {
			continue
		}
		fmt.Fprintf(&b, "/* %s */ .%schroma .%s%s { %s }\n", t, snippet.ChromaPrefix, snippet.ChromaPrefix, cls, css)
	}
	return b.String()
}
