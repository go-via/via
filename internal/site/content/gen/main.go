// Command gen writes content/api_gen.go, the exported API of every public via
// package, which the /reference page renders. Run it after changing via's
// exported API or its doc comments, and commit the result; a test in apigen
// fails while the file is stale.
//
//	go generate ./content
package main

import (
	"log"
	"os"

	"go-via.dev/site/content/gen/apigen"
)

func main() {
	syms, err := apigen.Load(apigen.Root())
	if err != nil {
		log.Fatal(err)
	}
	src, err := apigen.Render(syms)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(apigen.Out(), src, 0o644); err != nil {
		log.Fatal(err)
	}
}
