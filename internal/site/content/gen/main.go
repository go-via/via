// Command gen writes content/api_gen.go, the exported API of every public via
// package, which the /reference page renders. Run it after changing via's
// exported API or its doc comments, and commit the result; a test fails while
// the file is stale.
//
//	go generate ./content
package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
)

// here is this file's directory, not the working directory, so `go generate`
// and a hand-run from anywhere read and write the same files.
func here() string {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatal("gen: no caller information")
	}
	return filepath.Dir(self)
}

func out() string { return filepath.Join(here(), "..", "api_gen.go") }

func viaRoot() string { return filepath.Join(here(), "..", "..", "..", "..") }

func main() {
	syms, err := load(viaRoot())
	if err != nil {
		log.Fatal(err)
	}
	src, err := render(syms)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(out(), src, 0o644); err != nil {
		log.Fatal(err)
	}
}
