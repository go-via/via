package via_test

// These are source-text lints, not behaviour tests. They parse via's own and
// the examples' .go files and assert on what is written there — the identifiers
// used, the shape of an argument at a call site. Nothing here exercises the
// running system, and a rename or a refactor can break one without anything
// being wrong with via; that is the price of guarding a design constraint the
// type system cannot express. They live in their own file so no one mistakes
// them for tests of behaviour, and so a failure here is read as "the source
// drifted from the rule" rather than "via is broken".
//
// Each one guards a promise made in the README and the docs:
//   - reflection-free wiring (the reflect allowlist)
//   - no '&' and no closures where via takes a handler (the example lint)
//   - black-box tests, and none of package main (CONVENTIONS.md)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const viaPkg = "github.com/go-via/via"

// handlerSites are the via callees whose func arguments must be named method
// values; the value says whether '&' is refused too. Callees resolve through
// go/types, so an aliased or dot import is caught and an unrelated Listen or a
// local named via is not. Every func in package on binds a handler, so
// handlerSite matches that package whole. Ctx.Listen admits '&': its topic is
// a pointer.
var handlerSites = map[string]bool{
	viaPkg + ".Handler": true, viaPkg + ".Mount": true, viaPkg + ".Child": true,
	viaPkg + ".When": true, viaPkg + ".Each": true, viaPkg + ".On": true,
	viaPkg + ".OnArg": true, viaPkg + ".PostForm": true,
	viaPkg + ".List.Each":     true,
	viaPkg + ".Ctx.OnDispose": true, viaPkg + ".Ctx.Listen": false,
}

func TestExamples_takeNoAddressOfOrClosureAtViaCallSites(t *testing.T) {
	t.Parallel()
	// The site's demos and snippets are held to the same rule: each is shown
	// verbatim as documentation of the call-site shape. internal/site is its
	// own module, so it is listed from its own directory.
	for _, mod := range []struct {
		dir      string
		patterns []string
	}{
		{".", []string{"./internal/example/..."}},
		{filepath.Join("internal", "site"), []string{"./demos/...", "./snippet/src/..."}},
	} {
		listed := listPackages(t, mod.dir, mod.patterns...)
		fset := token.NewFileSet()
		imp := exportImporter(fset, listed)
		var linted int
		for _, p := range listed {
			if p.DepOnly {
				continue
			}
			linted++
			t.Run(p.ImportPath, func(t *testing.T) {
				var files []*ast.File
				for _, name := range p.GoFiles {
					f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, 0)
					require.NoError(t, err)
					files = append(files, f)
				}
				info, err := typeCheck(fset, imp, p.ImportPath, files)
				require.NoError(t, err)
				for _, v := range callSiteViolations(fset, files, info) {
					assert.Fail(t, v.msg, "%s", v.pos)
				}
			})
		}
		require.NotZero(t, linted, "expected example sources to lint under %s", mod.dir)
	}
}

func TestCallSiteLint_flagsOnlyViaHandlerCallees(t *testing.T) {
	t.Parallel()
	listed := listPackages(t, ".", ".", "./on", "./topic", "strings")
	const header = `package p

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/topic"
)

var (
	_  = on.Click[func(*via.Ctx)]
	_  = h.Str[string]
	tp = topic.New[int]()
)

type P struct {
	L via.List[int]
	C C
	T topic.Topic[int]
	N int
}

type C struct{}

func (C) View() h.H                  { return nil }
func (p *P) View() h.H               { return nil }
func (p *P) Inc(*via.Ctx)            {}
func (p *P) Pick(*via.Ctx, *int)     {}
func (p *P) recv(*via.Ctx, int)      {}
func (p *P) row(int) h.H             { return nil }
func (p *P) body() h.H               { return nil }
func (p *P) stop()                   {}
`
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"closure to via.When", header + `
func (p *P) V() h.H { return via.When(true, func() h.H { return nil }) }`,
			[]string{"closure passed to via.When: pass a named method value"}},
		{"closure through an aliased import", `package p

import (
	v "github.com/go-via/via"
	"github.com/go-via/via/h"
)

func V(xs []int) h.H { return v.Each[int](xs, func(int) h.H { return nil }) }`,
			[]string{"closure passed to via.Each: pass a named method value"}},
		{"closure to on.Click", header + `
func (p *P) V() h.H { return h.Button(on.Click(func(*via.Ctx) {})) }`,
			[]string{"closure passed to via/on.Click: pass a named method value"}},
		{"closure to Ctx.Listen and Ctx.OnDispose", header + `
func (p *P) OnInit(ctx *via.Ctx) error {
	ctx.Listen(tp, func(*via.Ctx, int) {})
	ctx.OnDispose(func() {})
	return nil
}`,
			[]string{
				"closure passed to via.Ctx.Listen: pass a named method value",
				"closure passed to via.Ctx.OnDispose: pass a named method value",
			}},
		{"closure to List.Each", header + `
func (p *P) V() h.H { return p.L.Each(func(int) h.H { return nil }) }`,
			[]string{"closure passed to via.List.Each: pass a named method value"}},
		{"address-of to on.WithArg", header + `
func (p *P) V() h.H { return h.Button(on.Click(on.WithArg(p.Pick, &p.N))) }`,
			[]string{"'&' passed to via/on.WithArg: via takes compositions by value"}},
		{"literal to via.Child", header + `
func (p *P) V() h.H { return via.Child(C{}) }`,
			[]string{"composite literal passed to via.Child: pass the parent's field (p.Chat)"}},
		{"named method values pass", header + `
func (p *P) OnInit(ctx *via.Ctx) error {
	ctx.Listen(&p.T, p.recv)
	ctx.OnDispose(p.stop)
	return nil
}

func (p *P) V() h.H {
	return h.Div(h.Button(on.Click(p.Inc)), p.L.Each(p.row), via.When(true, p.body), via.Child(p.C))
}`, nil},
		{"stdlib package imported as via", `package p

import via "strings"

var _ = via.Map(func(r rune) rune { return r }, "x")`, nil},
		{"local value named on", `package p

type fake struct{}

func (fake) Click(func()) {}

var on fake

func V() { on.Click(func() {}) }`, nil},
		{"Listen on a non-Ctx type", `package p

type bus struct{}

func (bus) Listen(func()) {}

func V() { bus{}.Listen(func() {}) }`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "p.go", tt.src, 0)
			require.NoError(t, err)
			info, err := typeCheck(fset, exportImporter(fset, listed), "p", []*ast.File{f})
			require.NoError(t, err)
			var got []string
			for _, v := range callSiteViolations(fset, []*ast.File{f}, info) {
				got = append(got, v.msg)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

type violation struct {
	pos token.Position
	msg string
}

func callSiteViolations(fset *token.FileSet, files []*ast.File, info *types.Info) []violation {
	var out []violation
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			key, noAddr, ok := handlerSite(info, call)
			if !ok {
				return true
			}
			name := strings.TrimPrefix(key, "github.com/go-via/")
			for _, arg := range call.Args {
				switch a := ast.Unparen(arg).(type) {
				case *ast.FuncLit:
					out = append(out, violation{fset.Position(a.Pos()),
						"closure passed to " + name + ": pass a named method value"})
				case *ast.UnaryExpr:
					if a.Op == token.AND && noAddr {
						out = append(out, violation{fset.Position(a.Pos()),
							"'&' passed to " + name + ": via takes compositions by value"})
					}
				case *ast.CompositeLit:
					// via.Child(p.Chat) embeds the parent's field; a literal
					// would re-seed a fresh child on every render.
					if key == viaPkg+".Child" {
						out = append(out, violation{fset.Position(a.Pos()),
							"composite literal passed to via.Child: pass the parent's field (p.Chat)"})
					}
				}
			}
			return true
		})
	}
	return out
}

func handlerSite(info *types.Info, call *ast.CallExpr) (key string, noAddr, ok bool) {
	fun := ast.Unparen(call.Fun)
	switch ix := fun.(type) {
	case *ast.IndexExpr:
		fun = ast.Unparen(ix.X)
	case *ast.IndexListExpr:
		fun = ast.Unparen(ix.X)
	}
	var id *ast.Ident
	switch f := fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return "", false, false
	}
	fn, isFunc := info.Uses[id].(*types.Func)
	if !isFunc || fn.Pkg() == nil {
		return "", false, false
	}
	fn = fn.Origin()
	key = fn.Pkg().Path() + "."
	if recv := fn.Signature().Recv(); recv != nil {
		rt := recv.Type()
		if p, isPtr := rt.(*types.Pointer); isPtr {
			rt = p.Elem()
		}
		named, isNamed := rt.(*types.Named)
		if !isNamed {
			return "", false, false
		}
		key += named.Origin().Obj().Name() + "."
	}
	key += fn.Name()
	if fn.Pkg().Path() == viaPkg+"/on" {
		return key, true, true
	}
	noAddr, ok = handlerSites[key]
	return key, noAddr, ok
}

type listedPackage struct {
	ImportPath, Dir, Export string
	GoFiles                 []string
	DepOnly                 bool
}

// -export builds each package's export data, so the lint type-checks only the
// packages it scans and reads every dependency the way the compiler built it.
func listPackages(t *testing.T, dir string, patterns ...string) []listedPackage {
	t.Helper()
	args := append([]string{"list", "-export", "-deps", "-json=ImportPath,Dir,Export,GoFiles,DepOnly"}, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	require.NoError(t, err, "go list in %s: %s", dir, stderr.String())
	var pkgs []listedPackage
	for dec := json.NewDecoder(bytes.NewReader(out)); dec.More(); {
		var p listedPackage
		require.NoError(t, dec.Decode(&p))
		pkgs = append(pkgs, p)
	}
	return pkgs
}

func exportImporter(fset *token.FileSet, pkgs []listedPackage) types.Importer {
	export := map[string]string{}
	for _, p := range pkgs {
		export[p.ImportPath] = p.Export
	}
	return importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		if export[path] == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(export[path])
	})
}

func typeCheck(fset *token.FileSet, imp types.Importer, path string, files []*ast.File) (*types.Info, error) {
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	_, err := (&types.Config{Importer: imp}).Check(path, fset, files, info)
	return info, err
}

func TestCore_importsNoReflectPackage(t *testing.T) {
	t.Parallel()
	// reflect is admitted in exactly three files and only on type-setup paths
	// that run once per composition type (Mount/Child) and are memoized: the
	// action-id func name and receiver field path, the field-name signal table
	// (including a SignalCS field's zero value), the child's parent field
	// lookup, and the hook-shape check. Nothing here may run per render — that
	// is the invariant this whitelist exists to keep honest.
	allowed := map[string][]string{
		"via.go": {"reflect.Array", "reflect.Map", "reflect.New", "reflect.Pointer", "reflect.PointerTo",
			"reflect.Slice", "reflect.Struct", "reflect.StructField", "reflect.Type",
			"reflect.TypeOf"},
		// action.go resolves a handler's Go name off its code pointer and its
		// receiver's field path off the unit type, once per (unit type, fn,
		// receiver offset) and memoized — see actionID. A value-receiver
		// handler's type walk is memoized per (unit type, fn) — see
		// valueMethodID.
		"action.go": {"reflect.Array", "reflect.Interface", "reflect.Struct",
			"reflect.StructField", "reflect.Type", "reflect.ValueOf"},
		"child.go": {"reflect.TypeOf"},
		// router.go's kind switch is the Assets constancy probe, which runs once
		// per Mount and never again.
		"router.go": {"reflect.Bool", "reflect.Float32", "reflect.Float64", "reflect.Int",
			"reflect.Int16", "reflect.Int32", "reflect.Int64", "reflect.Int8", "reflect.NewAt",
			"reflect.PointerTo", "reflect.String", "reflect.Struct", "reflect.Type",
			"reflect.TypeOf", "reflect.Uint", "reflect.Uint16", "reflect.Uint32",
			"reflect.Uint64", "reflect.Uint8", "reflect.Value", "reflect.ValueOf"},
	}
	files := coreGoFiles(t)
	require.NotEmpty(t, files, "expected core sources to scan")
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
			require.NoError(t, err)
			for _, imp := range f.Imports {
				if imp.Path.Value != `"reflect"` {
					continue
				}
				want, ok := allowed[filepath.Base(file)]
				require.True(t, ok, "%s imports reflect — via wiring must be reflection-free", file)
				src, err := os.ReadFile(file)
				require.NoError(t, err)
				uses := slices.Compact(slices.Sorted(slices.Values(
					regexp.MustCompile(`reflect\.\w+`).FindAllString(string(src), -1))))
				assert.Equal(t, want, uses, "%s may only reflect on the memoized type-setup path", file)
			}
		})
	}
}

func coreGoFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, dir := range []string{".", "expr", "h", "topic"} {
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		for _, e := range entries {
			n := e.Name()
			if !e.IsDir() && strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
				files = append(files, filepath.Join(dir, n))
			}
		}
	}
	return files
}

func TestCtx_doesNotExposeBinderPlumbing(t *testing.T) {
	t.Parallel()
	banned := map[string]bool{
		"Dyn": true, "DynAttr": true, "NewRenderer": true, "Renderer": true, "Binder": true,
	}
	fset := token.NewFileSet()
	entries, err := os.ReadDir("h")
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join("h", e.Name()), nil, 0)
		require.NoError(t, err)
		for _, decl := range f.Decls {
			var name string
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					name = d.Name.Name
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						name = ts.Name.Name
					}
				}
			}
			assert.Falsef(t, name != "" && banned[name],
				"h package must not export %q — it belongs to internal/hcore", name)
		}
	}
}

// The walk starts at the repo root rather than per go.mod, so internal/site
// and vtbrowser are covered by the same pass as the root module.
func TestTests_areBlackBox(t *testing.T) {
	t.Parallel()
	got, err := whiteBoxTests(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestWhiteBoxLint_flagsInPackageAndMainTests(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for name, src := range map[string]string{
		"white/p.go":         "package p",
		"white/p_test.go":    "package p",
		"black/p.go":         "package p",
		"black/p_test.go":    "package p_test",
		"tagged/p.go":        "//go:build linux\n\npackage p",
		"tagged/p_test.go":   "package p_test",
		"testonly/x_test.go": "package whatever",
		"cmd/main.go":        "package main",
		"cmd/main_test.go":   "package main_test",
		".hidden/p.go":       "package p",
		".hidden/p_test.go":  "package p",
		"_skip/p.go":         "package p",
		"_skip/p_test.go":    "package p",
		"testdata/p.go":      "package p",
		"testdata/p_test.go": "package p",
		"vendor/v/p.go":      "package p",
		"vendor/v/p_test.go": "package p",
	} {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(src+"\n"), 0o644))
	}
	got, err := whiteBoxTests(root)
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join("cmd", "main_test.go") + ": tests package main, which no test can import: move the logic to an importable package",
		filepath.Join("white", "p_test.go") + ": package p, want p_test",
	}, got)
}

func whiteBoxTests(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		if n := d.Name(); path != root && (strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") || n == "vendor" || n == "testdata") {
			return filepath.SkipDir
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		pkgs := map[string]bool{}
		tests := map[string]string{}
		fset := token.NewFileSet()
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			f, err := parser.ParseFile(fset, filepath.Join(path, e.Name()), nil, parser.PackageClauseOnly)
			if err != nil {
				return err
			}
			if strings.HasSuffix(e.Name(), "_test.go") {
				tests[e.Name()] = f.Name.Name
			} else {
				pkgs[f.Name.Name] = true
			}
		}
		// A test-only directory has no package to be inside of.
		if len(pkgs) == 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, name := range slices.Sorted(maps.Keys(tests)) {
			file := filepath.Join(rel, name)
			switch pkg := tests[name]; {
			case pkgs["main"]:
				out = append(out, file+": tests package main, which no test can import: move the logic to an importable package")
			case !pkgs[strings.TrimSuffix(pkg, "_test")] || !strings.HasSuffix(pkg, "_test"):
				want := slices.Sorted(maps.Keys(pkgs))[0] + "_test"
				out = append(out, file+": package "+pkg+", want "+want)
			}
		}
		return nil
	})
	return out, err
}
