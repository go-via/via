// Command gen writes static/chroma.css from the chroma style the snippet package
// highlights with. Run it after changing either, and commit the result:
// nothing generates CSS at build or at startup.
//
//	go generate ./snippet
package main

import (
	"log"
	"os"

	"go-via.dev/site/snippet/gen/chromagen"
)

func main() {
	if err := os.WriteFile(chromagen.Out(), []byte(chromagen.CSS()), 0o644); err != nil {
		log.Fatal(err)
	}
}
