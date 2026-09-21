package demo

import (
	"fmt"
	"slices"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"go-via.dev/site/demos"
)

// pageBG matches site.css's <pre>; the style's own dark shows as a seam.
const pageBG = "#0f1116"

// chromaSample covers token kinds a content-page snippet may use that no demo does.
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

// ChromaCSS is static/chroma.css: rules only for classes Go source can emit.
// chroma's WriteCSS would also write line-number and line-link rules.
func ChromaCSS() string {
	style := styles.Get(ChromaStyle)
	if style == nil {
		panic("demo: no chroma style " + ChromaStyle)
	}

	used := map[string]bool{}
	for _, src := range append(sourceText(), chromaSample) {
		it, err := lexers.Get("go").Tokenise(nil, src)
		if err != nil {
			panic(err)
		}
		for _, t := range it.Tokens() {
			if cls := class(t.Type); cls != "" {
				used[cls] = true
			}
		}
	}

	bg := style.Get(chroma.Background)
	var b strings.Builder
	fmt.Fprintf(&b, "/* Background */ .%sbg { color: %s; background-color: %s }\n", ChromaPrefix, bg.Colour, pageBG)
	fmt.Fprintf(&b, "/* PreWrapper */ .%schroma { color: %s; background-color: %s }\n", ChromaPrefix, bg.Colour, pageBG)

	types := make([]int, 0, len(chroma.StandardTypes))
	for t := range chroma.StandardTypes {
		types = append(types, int(t))
	}
	slices.Sort(types)
	for _, i := range types {
		t := chroma.TokenType(i)
		cls := chroma.StandardTypes[t]
		if cls == "" || !used[ChromaPrefix+cls] {
			continue
		}
		// Sub(bg): as chroma's formatter, don't repeat what the background carries.
		css := html.StyleEntryToCSS(style.Get(t).Sub(bg))
		if css == "" {
			continue
		}
		fmt.Fprintf(&b, "/* %s */ .%schroma .%s%s { %s }\n", t, ChromaPrefix, ChromaPrefix, cls, css)
	}
	return b.String()
}

func sourceText() []string {
	entries, err := demos.FS.ReadDir(".")
	if err != nil {
		panic(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		src, err := demos.FS.ReadFile(e.Name())
		if err != nil {
			panic(err)
		}
		out = append(out, string(src))
	}
	return out
}
