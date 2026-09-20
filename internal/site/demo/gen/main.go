// Command gen writes static/chroma.css from the chroma style the demo package
// highlights with. Run it from internal/site after changing either, and commit
// the result: nothing generates CSS at build or at startup.
//
//	go run ./demo/gen
package main

import (
	"log"
	"os"

	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"go-via.dev/site/demo"
)

func main() {
	style := styles.Get(demo.ChromaStyle)
	if style == nil {
		log.Fatalf("gen: no chroma style %q", demo.ChromaStyle)
	}

	f, err := os.Create("static/chroma.css")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	fm := html.New(html.WithClasses(true), html.WithLineNumbers(false), html.ClassPrefix(demo.ChromaPrefix))
	if err := fm.WriteCSS(f, style); err != nil {
		log.Fatal(err)
	}
}
