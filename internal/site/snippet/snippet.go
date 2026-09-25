// Package snippet shows Go the build compiles. Each directory under src/ is a
// package of this module, so go build, go vet and go test cover it, and the
// demos package's files are served too, under "demos/". A page names a file or
// a region of one; it never hand-types Go.
//
// A region is the lines between "// snippet:start <name>" and the next
// unmatched "// snippet:end". Regions nest, and every marker line is dropped
// from whatever is shown, so the reader never sees one.
package snippet

import (
	"embed"
	"io/fs"
	"path"
	"strings"
	"sync"

	"github.com/go-via/via/h"
	"go-via.dev/site/demos"
)

//go:embed src
var srcFS embed.FS

type file struct {
	lines   []string
	regions map[string][2]int
}

var files = map[string]*file{}

func init() {
	err := fs.WalkDir(srcFS, "src", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !embeddable(p) {
			return err
		}
		b, err := srcFS.ReadFile(p)
		if err != nil {
			return err
		}
		add(strings.TrimPrefix(p, "src/"), string(b))
		return nil
	})
	if err != nil {
		panic(err)
	}
	entries, err := demos.FS.ReadDir(".")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		if !embeddable(e.Name()) {
			continue
		}
		b, err := demos.FS.ReadFile(e.Name())
		if err != nil {
			panic(err)
		}
		add("demos/"+e.Name(), string(b))
	}
}

// A _test.go is compiled only by go test, so what it shows could have stopped
// building without go build noticing.
func embeddable(name string) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

func add(name, src string) {
	f := &file{regions: map[string][2]int{}}
	type open struct {
		name  string
		start int
	}
	var stack []open
	for _, line := range strings.Split(strings.TrimSuffix(src, "\n"), "\n") {
		marker, ok := strings.CutPrefix(strings.TrimSpace(line), "// snippet:")
		switch {
		case !ok:
			// A dropped marker between two blank lines would otherwise leave
			// a double one, which gofmt never writes.
			if strings.TrimSpace(line) == "" && len(f.lines) > 0 && strings.TrimSpace(f.lines[len(f.lines)-1]) == "" {
				continue
			}
			f.lines = append(f.lines, line)
		case strings.HasPrefix(marker, "start "):
			region := strings.TrimSpace(strings.TrimPrefix(marker, "start "))
			if region == "" {
				panic("snippet: " + name + ": snippet:start with no region name")
			}
			_, dup := f.regions[region]
			for _, o := range stack {
				dup = dup || o.name == region
			}
			if dup {
				panic("snippet: " + name + ": region " + `"` + region + `"` + " is declared twice")
			}
			stack = append(stack, open{region, len(f.lines)})
		case marker == "end":
			if len(stack) == 0 {
				panic("snippet: " + name + ": snippet:end with no open region")
			}
			o := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			f.regions[o.name] = [2]int{o.start, len(f.lines)}
		default:
			panic("snippet: " + name + ": unknown marker " + `"// snippet:` + marker + `"`)
		}
	}
	if len(stack) > 0 {
		panic("snippet: " + name + ": region " + `"` + stack[len(stack)-1].name + `"` + " is never closed")
	}
	files[name] = f
}

// Option adjusts one rendered block.
type Option func(*config)

type config struct {
	title  string
	titled bool
	marks  []string
}

// Title replaces the block's label, which defaults to the file's base name.
// An empty label leaves the header out.
func Title(label string) Option {
	return func(c *config) {
		if c.titled {
			panic("snippet: Title given twice")
		}
		c.title, c.titled = label, true
	}
}

// Mark highlights every line containing any of substrs. It matches text
// rather than line numbers, so an edit to the file cannot move the highlight
// onto the wrong line; a substr that matches no line panics.
func Mark(substrs ...string) Option {
	return func(c *config) { c.marks = append(c.marks, substrs...) }
}

// Show renders the whole file name, a path under src/ ("counter/main.go") or
// "demos/<file>". An unknown name panics, which fails site startup, since
// every page renders once to build the search index.
func Show(name string, opts ...Option) h.H {
	return cached(name, "", opts, func() []string { return lookup(name).lines })
}

// Region renders one named region of a file, dedented. An unknown file or
// region panics, as with Show.
func Region(name, region string, opts ...Option) h.H {
	return cached(name, region, opts, func() []string {
		f := lookup(name)
		span, ok := f.regions[region]
		if !ok {
			panic("snippet: " + name + " has no region " + `"` + region + `"`)
		}
		return dedent(f.lines[span[0]:span[1]])
	})
}

// Sources is the shown text of every file, which snippet/gen scans for the token
// classes static/chroma.css must style.
func Sources() []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, strings.Join(f.lines, "\n")+"\n")
	}
	return out
}

func lookup(name string) *file {
	f, ok := files[name]
	if !ok {
		panic("snippet: no embedded file named " + name)
	}
	return f
}

// h nodes are immutable, so one highlighted tree serves every render of a page.
var cache sync.Map

func cached(name, region string, opts []Option, lines func() []string) h.H {
	c := config{}
	for _, o := range opts {
		o(&c)
	}
	if !c.titled {
		c.title = path.Base(name)
	}
	key := name + "\x00" + region + "\x00" + c.title + "\x00" + strings.Join(c.marks, "\x00")
	if n, ok := cache.Load(key); ok {
		return n.(h.H)
	}
	ls := trimBlank(lines())
	where := name
	if region != "" {
		where += "#" + region
	}
	hl := make([]bool, len(ls))
	for _, m := range c.marks {
		hit := false
		for i, l := range ls {
			if strings.Contains(l, m) {
				hl[i], hit = true, true
			}
		}
		if !hit {
			panic("snippet: " + where + ": Mark " + `"` + m + `"` + " matches no line")
		}
	}
	n := frame(c.title, goPre(strings.Join(ls, "\n")+"\n", hl, len(c.marks) > 0))
	cache.Store(key, n)
	return n
}

func trimBlank(ls []string) []string {
	for len(ls) > 0 && strings.TrimSpace(ls[0]) == "" {
		ls = ls[1:]
	}
	for len(ls) > 0 && strings.TrimSpace(ls[len(ls)-1]) == "" {
		ls = ls[:len(ls)-1]
	}
	return ls
}

func dedent(ls []string) []string {
	prefix := ""
	first := true
	for _, l := range ls {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lead := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if first {
			prefix, first = lead, false
			continue
		}
		for !strings.HasPrefix(lead, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = strings.TrimPrefix(l, prefix)
	}
	return out
}
