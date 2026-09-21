// Command gen writes static/chroma.css from the chroma style the demo package
// highlights with. Run it after changing either, and commit the result:
// nothing generates CSS at build or at startup.
//
//	go generate ./demo
package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"

	"go-via.dev/site/demo"
)

// out is resolved from this file's own path, not the working directory, so
// `go generate` and a hand-run from anywhere write the same file.
func out() string {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatal("gen: no caller information")
	}
	return filepath.Join(filepath.Dir(self), "..", "..", "static", "chroma.css")
}

func main() {
	if err := os.WriteFile(out(), []byte(demo.ChromaCSS()), 0o644); err != nil {
		log.Fatal(err)
	}
}
