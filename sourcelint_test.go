package via_test

// These are source-text lints, not behaviour tests. They parse via's own and
// the site's demo and snippet .go files and assert on what is written there — the identifiers
// used, the shape of an argument at a call site. Nothing here exercises the
// running system, and a rename or a refactor can break one without anything
// being wrong with via; that is the price of guarding a design constraint the
// type system cannot express. They live in their own file so no one mistakes
// them for tests of behaviour, and so a failure here is read as "the source
// drifted from the rule" rather than "via is broken".
//
// Each lint owns its rule: the test name states it and the failure message
// says why and how to fix it. CONVENTIONS.md keeps only the rules that need
// judgment, and the lints that lean on one point to its section.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/printer"
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
	"unicode/utf8"

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
	viaPkg + ".When": true, viaPkg + ".Each": true, viaPkg + ".PostForm": true,
	viaPkg + ".List.Each":     true,
	viaPkg + ".Ctx.OnDispose": true, viaPkg + ".Ctx.Listen": false,
}

func TestDemos_takeNoAddressOfOrClosureAtViaCallSites(t *testing.T) {
	t.Parallel()
	// Each demo and snippet is shown verbatim as documentation of the
	// call-site shape. internal/site is its own module, so it is listed from
	// its own directory.
	listed := listPackages(t, filepath.Join("internal", "site"), "./demos/...", "./snippet/src/...")
	fset := token.NewFileSet()
	imp := exportImporter(fset, listed)
	var linted int
	for _, p := range listed {
		if p.DepOnly {
			continue
		}
		linted++
		t.Run(p.ImportPath, func(t *testing.T) {
			// sequential: the subtests share one importer, which is not safe for concurrent use
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
	require.NotZero(t, linted, "expected site demos and snippets to lint")
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
		{"address-of to on.Bind", header + `
func (p *P) V() h.H { return h.Button(on.Click(on.Bind(p.Pick, &p.N))) }`,
			[]string{"'&' passed to via/on.Bind: via takes compositions by value"}},
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
	GoFiles, Imports        []string
	DepOnly                 bool
}

// -export builds each package's export data, so the lint type-checks only the
// packages it scans and reads every dependency the way the compiler built it.
func listPackages(t *testing.T, dir string, patterns ...string) []listedPackage {
	t.Helper()
	args := append([]string{"list", "-export", "-deps", "-json=ImportPath,Dir,Export,GoFiles,Imports,DepOnly"}, patterns...)
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

func TestCore_reflectUseMatchesTheAllowlist(t *testing.T) {
	t.Parallel()
	// reflect is admitted in these five files. Its walks run once per
	// composition type and are memoized: the action-id func name and receiver
	// field path, the field-name signal table (including a SignalCS field's
	// zero value), the child's parent field lookup, the hook-shape check, and
	// Mount's Assets probe. A render may only read the memo keys — a bound
	// handler's code pointer (actionID) and a Child's type — and never walk;
	// that is the invariant this allowlist exists to keep honest.
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
		// hooks.go checks each composition type's hook methods once — see
		// checkHooks.
		"hooks.go": {"reflect.PointerTo", "reflect.Type", "reflect.TypeOf"},
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

func TestPublicAPI_exportsNoBinderPlumbing(t *testing.T) {
	t.Parallel()
	got, err := binderPlumbingExports(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestBinderPlumbingLint_flagsEveryExportedDeclOutsideInternal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"p.go":                 "package via\n\ntype Ctx struct{}\n\nfunc (*Ctx) Bind() {}",
		"h/p.go":               "package h\n\ntype (\n\tRenderer int\n\tFine int\n)",
		"h/p_test.go":          "package h_test\n\ntype Binder int",
		"on/p.go":              "package on\n\nvar (\n\tDyn, fine = 1, 2\n)\n\nconst DynAttr = 0",
		"vt/p.go":              "package vt\n\ntype T struct{}\n\nfunc (T) Binder() {}\n\nfunc NewRenderer() {}",
		"internal/render/p.go": "package render\n\ntype Binder interface{}",
		"cmd/main.go":          "package main\n\nvar Renderer = 1",
		".hidden/p.go":         "package p\n\ntype Binder int",
		"testdata/p.go":        "package p\n\ntype Binder int",
	})
	got, err := binderPlumbingExports(root)
	require.NoError(t, err)
	const why = " — binder plumbing belongs to internal/render"
	assert.Equal(t, []string{
		filepath.Join("h", "p.go") + ": exports Renderer" + why,
		filepath.Join("on", "p.go") + ": exports Dyn" + why,
		filepath.Join("on", "p.go") + ": exports DynAttr" + why,
		filepath.Join("vt", "p.go") + ": exports T.Binder" + why,
		filepath.Join("vt", "p.go") + ": exports NewRenderer" + why,
	}, got)
}

// Every importable package is public surface; internal/render is where the
// plumbing lives, and package main exports nothing anyone can import.
func binderPlumbingExports(root string) ([]string, error) {
	banned := map[string]bool{
		"Dyn": true, "DynAttr": true, "NewRenderer": true, "Renderer": true, "Binder": true,
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		n := d.Name()
		if d.IsDir() {
			if path != root && (skipDir(n) || n == "internal") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil || f.Name.Name == "main" {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		flag := func(qual, name string) {
			if banned[name] {
				out = append(out, rel+": exports "+qual+name+" — binder plumbing belongs to internal/render")
			}
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				qual := ""
				if d.Recv != nil {
					qual = recvName(d.Recv.List[0].Type) + "."
				}
				flag(qual, d.Name.Name)
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						flag("", s.Name.Name)
					case *ast.ValueSpec:
						for _, id := range s.Names {
							flag("", id.Name)
						}
					}
				}
			}
		}
		return nil
	})
	return out, err
}

func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return "?"
}

func TestTests_areBlackBox(t *testing.T) {
	t.Parallel()
	// The walk starts at the repo root rather than per go.mod, so internal/site
	// and vtbrowser are covered by the same pass as the root module.
	got, err := whiteBoxTests(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestWhiteBoxLint_flagsInPackageAndMainTests(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
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
	})
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
		if path != root && skipDir(d.Name()) {
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

func TestSources_holdNoFuncLiterals(t *testing.T) {
	t.Parallel()
	got, err := funcLiterals(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestFuncLiteralLint_flagsLiteralsOutsideTestsAndShownSnippets(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"p/p.go":           "package p\n\nvar f = func() {}",
		"p/named.go":       "package p\n\nfunc g() {}\n\nvar h = g",
		"p/p_test.go":      "package p_test\n\nvar f = func() {}",
		"snippet/src/x.go": "package x\n\nvar f = func() {}",
		"testdata/t.go":    "package t\n\nvar f = func() {}",
	})
	got, err := funcLiterals(root)
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join("p", "p.go") + ":3:9: func literal: use a named func, method value or typed value"}, got)
}

// funcLiterals lists every func literal under root outside tests. Named code
// has a name in a stack trace and a profile, a typed value states what it
// captures, and a closure on a hot path allocates per call. The site's
// snippet/src is exempt: it shows readers plain net/http, where a closure is
// the idiom they will meet.
func funcLiterals(root string) ([]string, error) {
	files, err := goFiles(root, false)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rel := range files {
		if strings.Contains(filepath.ToSlash(rel), "snippet/src/") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			return nil, err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.FuncLit); ok {
				p := fset.Position(lit.Pos())
				out = append(out, fmt.Sprintf("%s:%d:%d: func literal: use a named func, method value or typed value", rel, p.Line, p.Column))
			}
			return true
		})
	}
	return out, nil
}

func TestSources_takeAtMostFiveParams(t *testing.T) {
	t.Parallel()
	got, err := wideFuncs(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestParamLint_flagsFuncsOverFiveParams(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"p/five.go":   "package p\n\nfunc f(a, b, c int, d string, e bool) {}",
		"p/six.go":    "package p\n\nfunc g(a, b, c int, d string, e bool, f any) {}",
		"p/recv.go":   "package p\n\ntype T struct{}\n\nfunc (T) m(int, int, int, int, int) {}",
		"p/p_test.go": "package p_test\n\nfunc h(a, b, c, d, e, f int) {}",
	})
	got, err := wideFuncs(root)
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join("p", "six.go") + ":3:1: g takes 6 params; group the ones that travel together"}, got)
}

// wideFuncs lists every func under root outside tests that takes more than
// five params, the receiver not counted: a long param list is one func doing
// several jobs, and params that travel together become a struct.
func wideFuncs(root string) ([]string, error) {
	files, err := goFiles(root, false)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rel := range files {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if n := paramCount(fd.Type.Params); n > 5 {
				p := fset.Position(fd.Pos())
				out = append(out, fmt.Sprintf("%s:%d:%d: %s takes %d params; group the ones that travel together", rel, p.Line, p.Column, fd.Name.Name, n))
			}
		}
	}
	return out, nil
}

func paramCount(fl *ast.FieldList) int {
	n := 0
	for _, f := range fl.List {
		n += max(len(f.Names), 1)
	}
	return n
}

// goFiles lists the .go files under root, relative to it, across nested
// modules: the _test.go files when tests is set, the rest otherwise.
func goFiles(root string, tests bool) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") != tests {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		out = append(out, rel)
		return err
	})
	return out, err
}

// skipDir reports the dirs the go tool skips.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "vendor" || name == "testdata"
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(src+"\n"), 0o644))
	}
}

type parsedFile struct {
	rel  string
	fset *token.FileSet
	*ast.File
}

func (p parsedFile) at(n ast.Node) string {
	pos := p.fset.Position(n.Pos())
	return fmt.Sprintf("%s:%d:%d", p.rel, pos.Line, pos.Column)
}

func parseFiles(root string, tests bool) ([]parsedFile, error) {
	rels, err := goFiles(root, tests)
	if err != nil {
		return nil, err
	}
	out := make([]parsedFile, 0, len(rels))
	for _, rel := range rels {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		out = append(out, parsedFile{rel, fset, f})
	}
	return out, nil
}

// publicFiles are the non-test files a user of via can import: outside
// internal/ and not package main.
func publicFiles(root string) ([]parsedFile, error) {
	files, err := parseFiles(root, false)
	return slices.DeleteFunc(files, func(p parsedFile) bool {
		return p.Name.Name == "main" || slices.Contains(strings.Split(filepath.ToSlash(p.rel), "/"), "internal")
	}), err
}

func exportedFunc(fd *ast.FuncDecl) bool {
	return fd.Name.IsExported() && (fd.Recv == nil || ast.IsExported(recvName(fd.Recv.List[0].Type)))
}

// isVocabulary reports a func whose body returns one call that spells a name
// as its first argument, like h.Abbr's render.El("abbr", kids...): a table
// entry whose name is its doc and whose use is the caller's.
func isVocabulary(fd *ast.FuncDecl) bool {
	if fd.Body == nil || len(fd.Body.List) != 1 {
		return false
	}
	ret, ok := fd.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	call, ok := ret.Results[0].(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

// testingParams names a func's *testing.T, *testing.B, *testing.F and
// testing.TB params.
func testingParams(ft *ast.FuncType) []string {
	var names []string
	for _, f := range ft.Params.List {
		e := f.Type
		if star, ok := e.(*ast.StarExpr); ok {
			e = star.X
		}
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "testing" || !slices.Contains([]string{"T", "B", "F", "TB"}, sel.Sel.Name) {
			continue
		}
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
	}
	return names
}

// calledOn returns M when n is the call recv.M(…).
func calledOn(n ast.Node, recv string) (string, bool) {
	if es, ok := n.(*ast.ExprStmt); ok {
		n = es.X
	}
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if x, ok := sel.X.(*ast.Ident); !ok || x.Name != recv {
		return "", false
	}
	return sel.Sel.Name, true
}

func startsWithCall(body *ast.BlockStmt, recv, method string) bool {
	if len(body.List) == 0 {
		return false
	}
	m, ok := calledOn(body.List[0], recv)
	return ok && m == method
}

var failMethods = []string{"Error", "Errorf", "Fail", "FailNow", "Fatal", "Fatalf"}

func TestTests_pairWithTheSourceTheyDrive(t *testing.T) {
	t.Parallel()
	got, err := unpairedTests(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestParityLint_flagsTestsWithNoSourceOfTheirName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"via.go":                  "package via",
		"sourcelint_test.go":      "package via_test",
		"p/p.go":                  "package p",
		"p/p_test.go":             "package p_test",
		"p/feature_test.go":       "package p_test",
		"snippet/src/x/a.go":      "package x",
		"snippet/src/x/x_test.go": "package x_test",
		"testonly/t_test.go":      "package t_test",
	})
	got, err := unpairedTests(root)
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join("p", "feature_test.go") +
		": no feature.go beside it: move its tests into the _test.go of the file they drive"}, got)
}

// unpairedTests lists each x_test.go with no x.go beside it: a test grouped by
// feature rather than by the file it drives, so no one finds it by name.
// sourcelint_test.go pairs with every file, a snippet package is one page's
// files tested together, and a test-only dir has no source to pair with.
func unpairedTests(root string) ([]string, error) {
	srcs, err := goFiles(root, false)
	if err != nil {
		return nil, err
	}
	tests, err := goFiles(root, true)
	if err != nil {
		return nil, err
	}
	dirs := map[string]bool{}
	for _, s := range srcs {
		dirs[filepath.Dir(s)] = true
	}
	var out []string
	for _, rel := range tests {
		src := strings.TrimSuffix(rel, "_test.go") + ".go"
		switch {
		case slices.Contains(srcs, src), rel == "sourcelint_test.go", !dirs[filepath.Dir(rel)],
			strings.Contains(filepath.ToSlash(rel), "snippet/src/"):
		default:
			out = append(out, rel+": no "+filepath.Base(src)+" beside it: move its tests into the _test.go of the file they drive")
		}
	}
	return out, nil
}

func TestTests_nameASubjectAndAClaim(t *testing.T) {
	t.Parallel()
	got, err := badTestNames(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestNameLint_flagsTestsNotNamedSubjectUnderscoreClaim(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{"p/p_test.go": `package p_test

import "testing"

func TestSignal_panicsOnABadTag(t *testing.T) {}

func TestSignal(t *testing.T) {}

func Test_signal_return(t *testing.T) {}

func TestMain(m *testing.M) {}`})
	got, err := badTestNames(root)
	require.NoError(t, err)
	const fix = ": name it Test<Subject>_<claim>, like TestSignal_panicsOnAMalformedTag"
	file := filepath.Join("p", "p_test.go")
	assert.Equal(t, []string{file + ":7:1: TestSignal" + fix, file + ":9:1: Test_signal_return" + fix}, got)
}

var testName = regexp.MustCompile(`^Test[A-Z][A-Za-z0-9]*_[a-z][A-Za-z0-9]*$`)

// badTestNames lists tests not named Test<Subject>_<claim>. The name is a
// test's only comment, so it states the behaviour the test proves.
func badTestNames(root string) ([]string, error) {
	files, err := parseFiles(root, true)
	var out []string
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || !strings.HasPrefix(fd.Name.Name, "Test") || fd.Name.Name == "TestMain" || testName.MatchString(fd.Name.Name) {
				continue
			}
			out = append(out, f.at(fd)+": "+fd.Name.Name+": name it Test<Subject>_<claim>, like TestSignal_panicsOnAMalformedTag")
		}
	}
	return out, err
}

func TestTests_failThroughTestify(t *testing.T) {
	t.Parallel()
	got, err := rawFailures(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestRawFailureLint_flagsTestingFailCallsInTests(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"p/p.go": "package p\n\nimport \"testing\"\n\nfunc H(t testing.TB) { t.Fatal() }",
		"p/p_test.go": `package p_test

import "testing"

func TestA_b(t *testing.T) {
	t.Logf("fine")
	t.Fatal("x")
	t.Run("s", func(st *testing.T) { st.Errorf("y") })
}

func BenchmarkA(b *testing.B) { b.Fatalf("z") }`,
	})
	got, err := rawFailures(root)
	require.NoError(t, err)
	const fix = ": fail through testify's assert or require"
	file := filepath.Join("p", "p_test.go")
	assert.Equal(t, []string{file + ":7:2: t.Fatal" + fix, file + ":8:35: st.Errorf" + fix, file + ":11:33: b.Fatalf" + fix}, got)
}

// rawFailures lists t.Fatal, t.Error and kin in tests. testify's assert and
// require print what was expected next to what came, so every failure reads
// the same way.
func rawFailures(root string) ([]string, error) {
	files, err := parseFiles(root, true)
	var out []string
	for _, f := range files {
		var recvs []string
		ast.Inspect(f.File, func(n ast.Node) bool {
			if ft, ok := n.(*ast.FuncType); ok {
				recvs = append(recvs, testingParams(ft)...)
			}
			return true
		})
		recvs = slices.Compact(slices.Sorted(slices.Values(recvs)))
		ast.Inspect(f.File, func(n ast.Node) bool {
			if _, ok := n.(*ast.CallExpr); !ok {
				return true
			}
			for _, r := range recvs {
				if m, ok := calledOn(n, r); ok && slices.Contains(failMethods, m) {
					out = append(out, f.at(n)+": "+r+"."+m+": fail through testify's assert or require")
				}
			}
			return true
		})
	}
	return out, err
}

func TestTests_sendThroughTheirOwnTransport(t *testing.T) {
	t.Parallel()
	got, err := sharedTransports(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSharedTransportLint_flagsWhatReachesHTTPDefaultTransport(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{"p/p_test.go": `package p_test

import (
	nh "net/http"
	"net/http/httptest"
)

func get(srv *httptest.Server) {
	nh.Get(srv.URL)
	nh.DefaultClient.Do(nil)
	_ = &nh.Client{CheckRedirect: nil}
	nh.DefaultTransport.RoundTrip(nil)
	_ = nh.DefaultTransport.(*nh.Transport).Clone()
	_ = &nh.Client{Transport: srv.Client().Transport}
	srv.Client().Get(srv.URL)
}`})
	got, err := sharedTransports(root)
	require.NoError(t, err)
	const fix = ": sends through http.DefaultTransport, whose idle connections every httptest.Server.Close closes; use srv.Client() or a cloned transport"
	file := filepath.Join("p", "p_test.go")
	assert.Equal(t, []string{file + ":9:2: nh.Get" + fix, file + ":10:2: nh.DefaultClient" + fix, file + ":11:7: nh.Client{}" + fix, file + ":12:2: nh.DefaultTransport" + fix}, got)
}

// sharedTransports lists test code that sends through http.DefaultTransport:
// DefaultClient, the package-level Get and kin, a Client with no Transport,
// and DefaultTransport itself unless it is being cloned.
func sharedTransports(root string) ([]string, error) {
	files, err := parseFiles(root, true)
	var out []string
	for _, f := range files {
		pkg := importName(f.File, "net/http")
		if pkg == "" {
			continue
		}
		cloned := map[ast.Node]bool{}
		ast.Inspect(f.File, func(n ast.Node) bool {
			if ta, ok := n.(*ast.TypeAssertExpr); ok {
				cloned[ta.X] = true
			}
			name := ""
			switch n := n.(type) {
			case *ast.SelectorExpr:
				if x, ok := n.X.(*ast.Ident); ok && x.Name == pkg && !cloned[n] {
					switch n.Sel.Name {
					case "DefaultClient", "DefaultTransport", "Get", "Post", "Head", "PostForm":
						name = pkg + "." + n.Sel.Name
					}
				}
			case *ast.CompositeLit:
				if sel, ok := n.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Client" && !hasKey(n, "Transport") {
					if x, ok := sel.X.(*ast.Ident); ok && x.Name == pkg {
						name = pkg + ".Client{}"
					}
				}
			}
			if name != "" {
				out = append(out, f.at(n)+": "+name+": sends through http.DefaultTransport, whose idle connections every httptest.Server.Close closes; use srv.Client() or a cloned transport")
			}
			return true
		})
	}
	return out, err
}

// importName is the name f refers to path by, or "" when f does not import it.
func importName(f *ast.File, path string) string {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != path {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return filepath.Base(path)
	}
	return ""
}

func hasKey(lit *ast.CompositeLit, key string) bool {
	return slices.ContainsFunc(lit.Elts, func(e ast.Expr) bool {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			return false
		}
		id, ok := kv.Key.(*ast.Ident)
		return ok && id.Name == key
	})
}

func TestTests_runInParallelOrSayWhyNot(t *testing.T) {
	t.Parallel()
	got, err := sequentialTests(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSequentialLint_flagsTestsThatNeitherRunInParallelNorSayWhy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{"p/p_test.go": `package p_test

import (
	"testing"
	"testing/synctest"
)

func TestA_parallel(t *testing.T) {
	t.Parallel()
	t.Run("bare", func(t *testing.T) {})
	t.Run("marked", func(t *testing.T) {
		// sequential: shares the counter
	})
}

func TestA_bare(t *testing.T) {}

func TestA_setenv(t *testing.T) { t.Setenv("K", "v") }

func TestA_bubble(t *testing.T) { synctest.Test(t, func(t *testing.T) {}) }

func TestA_emptyReason(t *testing.T) {
	// sequential:
	_ = 1
}`})
	got, err := sequentialTests(root)
	require.NoError(t, err)
	const fix = ": start with t.Parallel(), or open with // sequential: <the state it shares>"
	file := filepath.Join("p", "p_test.go")
	assert.Equal(t, []string{
		file + ":16:1: TestA_bare" + fix,
		file + ":22:1: TestA_emptyReason" + fix,
		file + ":10:16: subtest" + fix,
	}, got)
}

// sequentialTests lists tests and subtests that neither start with
// t.Parallel() nor open with a "// sequential: <reason>" line. Parallel tests
// surface races and keep the suite fast, so a test that cannot run in
// parallel names the state it shares. A test that calls t.Setenv cannot run
// in parallel, and synctest.Test forbids t.Parallel inside its bubble.
func sequentialTests(root string) ([]string, error) {
	files, err := parseFiles(root, true)
	const fix = ": start with t.Parallel(), or open with // sequential: <the state it shares>"
	var out []string
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Body == nil || !strings.HasPrefix(fd.Name.Name, "Test") || fd.Name.Name == "TestMain" {
				continue
			}
			if ts := testingParams(fd.Type); len(ts) == 1 && !parallelOrSaysWhy(f.File, fd.Body, ts[0]) {
				out = append(out, f.at(fd)+": "+fd.Name.Name+fix)
			}
		}
		ast.Inspect(f.File, func(n ast.Node) bool {
			if lit, t, ok := subtest(n); ok && !parallelOrSaysWhy(f.File, lit.Body, t) {
				out = append(out, f.at(lit)+": subtest"+fix)
			}
			return true
		})
	}
	return out, err
}

func subtest(n ast.Node) (*ast.FuncLit, string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) != 2 {
		return nil, "", false
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Run" {
		return nil, "", false
	}
	lit, ok := call.Args[1].(*ast.FuncLit)
	if !ok {
		return nil, "", false
	}
	ts := testingParams(lit.Type)
	if len(ts) != 1 {
		return nil, "", false
	}
	return lit, ts[0], true
}

func parallelOrSaysWhy(f *ast.File, body *ast.BlockStmt, t string) bool {
	if startsWithCall(body, t, "Parallel") || len(body.List) == 1 && startsWithCall(body, "synctest", "Test") {
		return true
	}
	end := body.Rbrace
	if len(body.List) > 0 {
		end = body.List[0].Pos()
	}
	for _, cg := range f.Comments {
		if cg.Pos() > body.Lbrace && cg.Pos() < end {
			if reason, ok := strings.CutPrefix(cg.List[0].Text, "// sequential:"); ok && strings.TrimSpace(reason) != "" {
				return true
			}
			break
		}
	}
	setenv := false
	ast.Inspect(body, func(n ast.Node) bool {
		if m, ok := calledOn(n, t); ok && m == "Setenv" {
			setenv = true
		}
		return !setenv
	})
	return setenv
}

func TestHelpers_markThemselvesHelpers(t *testing.T) {
	t.Parallel()
	got, err := unmarkedHelpers(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestHelperLint_flagsFailingHelpersWithoutTHelper(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{"p/p_test.go": `package p_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func serve(t *testing.T) { t.Helper(); t.Fatal("x") }

func open(t *testing.T) { require.NoError(t, nil) }

func fail(tb testing.TB) {
	tb.Fatalf("y")
}

func quiet(t *testing.T) { t.Log("z") }

func TestA_b(t *testing.T) { t.Fatal() }`})
	got, err := unmarkedHelpers(root)
	require.NoError(t, err)
	file := filepath.Join("p", "p_test.go")
	assert.Equal(t, []string{
		file + ":11:1: open fails the test: call t.Helper() first so the failure names its caller's line",
		file + ":13:1: fail fails the test: call tb.Helper() first so the failure names its caller's line",
	}, got)
}

// unmarkedHelpers lists test helpers that can fail the test but do not start
// with t.Helper(), so a failure would point inside the helper.
func unmarkedHelpers(root string) ([]string, error) {
	files, err := parseFiles(root, true)
	var out []string
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Body == nil || slices.ContainsFunc([]string{"Test", "Benchmark", "Fuzz", "Example"}, func(p string) bool {
				return strings.HasPrefix(fd.Name.Name, p)
			}) {
				continue
			}
			for _, t := range testingParams(fd.Type) {
				if failsThrough(fd.Body, t) && !startsWithCall(fd.Body, t, "Helper") {
					out = append(out, f.at(fd)+": "+fd.Name.Name+" fails the test: call "+t+".Helper() first so the failure names its caller's line")
					break
				}
			}
		}
	}
	return out, err
}

func failsThrough(body *ast.BlockStmt, t string) bool {
	fails := false
	ast.Inspect(body, func(n ast.Node) bool {
		if m, ok := calledOn(n, t); ok && slices.Contains(failMethods, m) {
			fails = true
		}
		if call, ok := n.(*ast.CallExpr); ok && len(call.Args) > 0 {
			sel, isSel := call.Fun.(*ast.SelectorExpr)
			arg, isArg := call.Args[0].(*ast.Ident)
			if isSel && isArg && arg.Name == t {
				if pkg, ok := sel.X.(*ast.Ident); ok && (pkg.Name == "assert" || pkg.Name == "require") {
					fails = true
				}
			}
		}
		return !fails
	})
	return fails
}

// allowedImports is every in-repo import edge of via's importable packages.
// A new edge is a design decision, made here in review. A package split out
// of root never imports it (CONVENTIONS.md, Internal Packages).
var allowedImports = map[string][]string{
	"via":              {"expr", "h", "internal/render", "internal/session", "topic"},
	"expr":             nil,
	"h":                {"internal/render"},
	"internal/render":  nil,
	"internal/session": nil,
	"on":               {"expr", "h", "internal/render", "via"},
	"topic":            nil,
	"vt":               nil,
	"vtbrowser":        nil,
}

func TestPackages_importOnlyWhatTheTableAdmits(t *testing.T) {
	t.Parallel()
	pkgs := append(listPackages(t, ".", "./..."), listPackages(t, "vtbrowser", "./...")...)
	assert.Empty(t, importDrift(pkgs, allowedImports))
}

func TestImportLint_flagsEdgesTheTableDoesNotAdmit(t *testing.T) {
	t.Parallel()
	pkgs := []listedPackage{
		{ImportPath: viaPkg, Imports: []string{"fmt", viaPkg + "/h"}},
		{ImportPath: viaPkg + "/h", Imports: []string{viaPkg + "/internal/render"}},
		{ImportPath: viaPkg + "/internal/render", Imports: []string{viaPkg}},
		{ImportPath: viaPkg + "/new"},
		{ImportPath: "fmt", DepOnly: true},
	}
	allowed := map[string][]string{"via": {"h", "topic"}, "h": nil, "internal/render": {"via"}}
	assert.Equal(t, []string{
		"allowedImports admits via → topic, which it no longer imports",
		"h imports internal/render, which allowedImports does not admit",
		"internal/render imports via: a package split out of root never imports it",
		"new is not in allowedImports: add its row",
	}, importDrift(pkgs, allowed))
}

func importDrift(pkgs []listedPackage, allowed map[string][]string) []string {
	var out []string
	for _, p := range pkgs {
		if p.DepOnly {
			continue
		}
		name := repoName(p.ImportPath)
		var got []string
		for _, imp := range p.Imports {
			if imp == viaPkg || strings.HasPrefix(imp, viaPkg+"/") {
				got = append(got, repoName(imp))
			}
		}
		if strings.HasPrefix(name, "internal/") && slices.Contains(got, "via") {
			out = append(out, name+" imports via: a package split out of root never imports it")
		}
		want, ok := allowed[name]
		if !ok {
			out = append(out, name+" is not in allowedImports: add its row")
			continue
		}
		for _, g := range got {
			if !slices.Contains(want, g) {
				out = append(out, name+" imports "+g+", which allowedImports does not admit")
			}
		}
		for _, w := range want {
			if !slices.Contains(got, w) {
				out = append(out, "allowedImports admits "+name+" → "+w+", which it no longer imports")
			}
		}
	}
	return out
}

func repoName(path string) string {
	if path == viaPkg {
		return "via"
	}
	return strings.TrimPrefix(path, viaPkg+"/")
}

func TestPublicAPI_namesNoInternalType(t *testing.T) {
	t.Parallel()
	got, err := internalLeaks(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestLeakLint_flagsExportedSignaturesNamingInternalTypes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"internal/render/r.go": "package render\n\ntype H interface{}\n\nfunc Raw() H { return nil }",
		"h/h.go": `package h

import "github.com/go-via/via/internal/render"

type H = render.H

func Div(kids ...H) H { return nil }

func Raw() render.H { return nil }

func hidden(render.H) {}`,
	})
	got, err := internalLeaks(root)
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join("h", "h.go") +
		":9:12: Raw names render.H, which a caller outside via cannot name: re-export it with an alias"}, got)
}

// internalLeaks lists exported funcs whose signature names a type from an
// internal/ package: a caller in another module cannot name it, so it cannot
// hold the value in a variable or return it from a helper.
func internalLeaks(root string) ([]string, error) {
	files, err := publicFiles(root)
	var out []string
	for _, f := range files {
		internal := map[string]bool{}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !slices.Contains(strings.Split(path, "/"), "internal") {
				continue
			}
			name := path[strings.LastIndex(path, "/")+1:]
			if imp.Name != nil {
				name = imp.Name.Name
			}
			internal[name] = true
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || !exportedFunc(fd) {
				continue
			}
			ast.Inspect(fd.Type, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); ok && internal[x.Name] {
					out = append(out, f.at(sel)+": "+fd.Name.Name+" names "+x.Name+"."+sel.Sel.Name+
						", which a caller outside via cannot name: re-export it with an alias")
				}
				return true
			})
		}
	}
	return out, err
}

func TestHandles_keepTheirFieldsUnexported(t *testing.T) {
	t.Parallel()
	got, err := exportedHandleFields(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestHandleLint_flagsExportedFieldsOnGenericStructs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{"p.go": `package via

type Signal[T any] struct {
	id  string
	Val T
}

type List[E any] struct {
	Signal[[]E]
}

type Meta struct{ Title string }`})
	got, err := exportedHandleFields(root)
	require.NoError(t, err)
	assert.Equal(t, []string{"p.go:5:2: Signal.Val is exported: via stamps a handle's wire name at mount, " +
		"and a caller who can set its fields can desync or forge it"}, got)
}

// exportedHandleFields lists exported fields of exported generic structs, the
// shape of via's composition handles (Signal, State, List, Topic). Embedding
// another handle is how List reuses State, so embedded fields pass.
func exportedHandleFields(root string) ([]string, error) {
	files, err := publicFiles(root)
	var out []string
	for _, f := range files {
		ast.Inspect(f.File, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok || ts.TypeParams == nil || !ts.Name.IsExported() {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if name.IsExported() {
						out = append(out, f.at(name)+": "+ts.Name.Name+"."+name.Name+" is exported: via stamps a handle's wire name at mount, "+
							"and a caller who can set its fields can desync or forge it")
					}
				}
			}
			return true
		})
	}
	return out, err
}

func TestHooks_areThePinnedDuckTypedMethods(t *testing.T) {
	t.Parallel()
	got, err := duckTypedMethods(".")
	require.NoError(t, err)
	// View, OnInit, OnReload and PageMeta are the composition hooks.
	// LoadVersion and SaveIf are VersionedSessionStore, an optional store
	// capability. FlushError and Unwrap probe the http.ResponseWriter.
	assert.Equal(t, []string{"FlushError", "LoadVersion", "OnInit", "OnReload", "PageMeta", "SaveIf", "Unwrap", "View"}, got,
		"a new duck-typed method must clear CONVENTIONS.md, Duck-Typed Methods")
}

func TestDuckTypeLint_listsExportedMethodsRootAssertsOn(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"p.go": `package via

type viewer interface{ View() string }

type hidden interface{ mark() }

type unused interface{ Title() string }

type ptrViewer[T any] = interface{ Render() T }

func probe(v any) {
	_, _ = v.(viewer)
	_, _ = v.(hidden)
	_, _ = v.(ptrViewer[int])
	switch v.(type) {
	case interface{ Close() error }:
	}
}`,
		"p_test.go": "package via_test\n\ntype t interface{ Foo() }\n\nvar _, _ = any(nil).(t)",
		"sub/s.go":  "package sub\n\ntype s interface{ Bar() }\n\nvar _, _ = any(nil).(s)",
	})
	got, err := duckTypedMethods(root)
	require.NoError(t, err)
	assert.Equal(t, []string{"Close", "Render", "View"}, got)
}

// duckTypedMethods lists the exported methods of every interface root type-
// asserts on: the methods via looks for on a user's type.
func duckTypedMethods(dir string) ([]string, error) {
	files, err := parseFiles(dir, false)
	files = slices.DeleteFunc(files, func(p parsedFile) bool { return filepath.Dir(p.rel) != "." })
	ifaces := map[string]*ast.InterfaceType{}
	for _, f := range files {
		ast.Inspect(f.File, func(n ast.Node) bool {
			if ts, ok := n.(*ast.TypeSpec); ok {
				if it, ok := ts.Type.(*ast.InterfaceType); ok {
					ifaces[ts.Name.Name] = it
				}
			}
			return true
		})
	}
	resolve := func(e ast.Expr) *ast.InterfaceType {
		if ix, ok := e.(*ast.IndexExpr); ok {
			e = ix.X
		}
		switch t := e.(type) {
		case *ast.InterfaceType:
			return t
		case *ast.Ident:
			return ifaces[t.Name]
		}
		return nil
	}
	var out []string
	for _, f := range files {
		ast.Inspect(f.File, func(n ast.Node) bool {
			var asserted []ast.Expr
			switch n := n.(type) {
			case *ast.TypeAssertExpr:
				asserted = []ast.Expr{n.Type}
			case *ast.CaseClause:
				asserted = n.List
			}
			for _, e := range asserted {
				if it := resolve(e); it != nil {
					for _, m := range it.Methods.List {
						if len(m.Names) == 1 && m.Names[0].IsExported() {
							out = append(out, m.Names[0].Name)
						}
					}
				}
			}
			return true
		})
	}
	return slices.Compact(slices.Sorted(slices.Values(out))), err
}

func TestExports_carryADocComment(t *testing.T) {
	t.Parallel()
	got, err := undocumentedExports(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestDocLint_flagsExportsWithNoDocCommentButVocabulary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{"p/p.go": `package p

// Documented has a doc.
func Documented() {}

func Bare() {}

func Tag(kids ...any) any { return el("tag", kids...) }

func el(string, ...any) any { return nil }

// Group docs every spec in it.
const (
	A = 1
	B = 2
)

var (
	// C has its own.
	C = 3
	D = 4
)

type T struct{}

func (T) M() {}

type hidden struct{}

func (hidden) M() {}`})
	got, err := undocumentedExports(root)
	require.NoError(t, err)
	const fix = " is exported with no doc comment: state its contract or caveat"
	file := filepath.Join("p", "p.go")
	assert.Equal(t, []string{file + ":6:1: Bare" + fix, file + ":21:2: D" + fix, file + ":24:6: T" + fix, file + ":26:1: T.M" + fix}, got)
}

// undocumentedExports lists exported declarations with no doc comment. A
// vocabulary entry (see isVocabulary) is exempt: its name is its doc, and a
// comment would only restate it.
func undocumentedExports(root string) ([]string, error) {
	files, err := publicFiles(root)
	const fix = " is exported with no doc comment: state its contract or caveat"
	var out []string
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if exportedFunc(d) && d.Doc == nil && !isVocabulary(d) {
					out = append(out, f.at(d)+": "+qualified(d)+fix)
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					for _, name := range specNames(spec) {
						if name.IsExported() && d.Doc == nil && specDoc(spec) == nil {
							out = append(out, f.at(name)+": "+name.Name+fix)
						}
					}
				}
			}
		}
	}
	return out, err
}

func qualified(fd *ast.FuncDecl) string {
	if fd.Recv == nil {
		return fd.Name.Name
	}
	return recvName(fd.Recv.List[0].Type) + "." + fd.Name.Name
}

func specNames(spec ast.Spec) []*ast.Ident {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return []*ast.Ident{s.Name}
	case *ast.ValueSpec:
		return s.Names
	}
	return nil
}

func specDoc(spec ast.Spec) *ast.CommentGroup {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return s.Doc
	case *ast.ValueSpec:
		return s.Doc
	}
	return nil
}

// pinnedErrors is every error value and error type in via's modules.
var pinnedErrors = []string{
	"internal/render.Miswired", "via.ErrForbidden", "via.ErrNotFound", "via.ErrStaleTab", "via.ErrStoreDown",
	"via.errRedirected",
}

func TestErrors_areThePinnedSet(t *testing.T) {
	t.Parallel()
	got, err := errorDecls(".")
	require.NoError(t, err)
	assert.Equal(t, pinnedErrors, got,
		"a new error value or type must clear CONVENTIONS.md, No Custom Error Types, then join pinnedErrors")
}

func TestErrorLint_listsSentinelsAndErrorTypes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"p/p.go": `package p

import (
	"errors"
	"fmt"
)

var ErrGone = errors.New("gone")

var errQuiet, n = fmt.Errorf("quiet"), 1

type Miswired struct{}

func (Miswired) Error() string { return "" }

type notAnError struct{}

func (notAnError) Error(int) string { return "" }`,
		"r.go":               "package via\n\nimport \"errors\"\n\nvar ErrRoot = errors.New(\"root\")",
		"snippet/src/x/x.go": "package x\n\nimport \"errors\"\n\nvar ErrShown = errors.New(\"shown\")",
	})
	got, err := errorDecls(root)
	require.NoError(t, err)
	assert.Equal(t, []string{"p.ErrGone", "p.Miswired", "p.errQuiet", "via.ErrRoot"}, got)
}

// errorDecls lists package-level errors.New and fmt.Errorf values and types
// with an Error() string method, as dir.Name. The site's snippet/src shows
// readers plain Go, not via's own errors.
func errorDecls(root string) ([]string, error) {
	files, err := parseFiles(root, false)
	var out []string
	for _, f := range files {
		if strings.Contains(filepath.ToSlash(f.rel), "snippet/src/") {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(f.rel))
		if dir == "." {
			dir = "via"
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil && d.Name.Name == "Error" && d.Type.Params.NumFields() == 0 &&
					d.Type.Results.NumFields() == 1 && types.ExprString(d.Type.Results.List[0].Type) == "string" {
					out = append(out, dir+"."+recvName(d.Recv.List[0].Type))
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, v := range vs.Values {
						if name := types.ExprString(v); strings.HasPrefix(name, "errors.New(") || strings.HasPrefix(name, "fmt.Errorf(") {
							out = append(out, dir+"."+vs.Names[i].Name)
						}
					}
				}
			}
		}
	}
	return slices.Sorted(slices.Values(out)), err
}

func TestMarkdown_staysWithin80Columns(t *testing.T) {
	t.Parallel()
	got, err := wideMarkdown(".")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestMarkdownLint_flagsProseOver80ColumnsOnly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	long := strings.Repeat("a ", 41)
	writeTree(t, root, map[string]string{
		"doc.md": "# T\n\n" + long + "\n\n```\n" + strings.Repeat("b", 90) + "\n```\n\n" +
			"https://example.com/" + strings.Repeat("c", 80) + "\n\n[ref]: https://example.com/" + strings.Repeat("d", 80) + "\n\n" +
			strings.Repeat("é", 80),
		"testdata/x.md": long,
		".hidden/x.md":  long,
	})
	got, err := wideMarkdown(root)
	require.NoError(t, err)
	assert.Equal(t, []string{"doc.md:3: 82 columns: wrap at 80"}, got)
}

var linkDef = regexp.MustCompile(`^\s*\[[^\]]+\]:\s+\S+$`)

// wideMarkdown lists markdown lines over 80 columns. Code blocks keep their
// width, and a bare URL or link definition cannot break.
func wideMarkdown(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (skipDir(d.Name()) || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		fenced := false
		for i, line := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				fenced = !fenced
				continue
			}
			bare := len(strings.Fields(line)) == 1 && strings.Contains(line, "://")
			if n := utf8.RuneCountInString(line); n > 80 && !fenced && !bare && !linkDef.MatchString(line) {
				out = append(out, fmt.Sprintf("%s:%d: %d columns: wrap at 80", rel, i+1, n))
			}
		}
		return err
	})
	return out, err
}

// directRequires pins each module's direct dependencies. Every user of the
// module builds them, so adding one is a design decision, made here in review.
var directRequires = map[string][]string{
	".":             {"github.com/stretchr/testify"},
	"vtbrowser":     {"github.com/chromedp/cdproto", "github.com/chromedp/chromedp", "github.com/go-via/via", "github.com/stretchr/testify"},
	"internal/site": {"github.com/alecthomas/chroma/v2", "github.com/go-via/via", "github.com/stretchr/testify"},
}

func TestModules_requireOnlyThePinnedDependencies(t *testing.T) {
	t.Parallel()
	for dir, want := range directRequires {
		src, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		require.NoError(t, err)
		assert.Equal(t, want, requires(string(src)),
			"%s/go.mod: a new direct dependency is a design decision: pin it in directRequires", dir)
	}
}

func TestRequireLint_readsDirectRequiresOnly(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"a.example/one", "b.example/two"}, requires("module m\n\nrequire a.example/one v1.0.0\n\n"+
		"require (\n\tb.example/two v0.1.0\n\tc.example/three v1.0.0 // indirect\n)\n\ntool (\n\td.example/four\n)\n"))
}

func requires(gomod string) []string {
	var out []string
	block := false
	for _, line := range strings.Split(gomod, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "require (":
			block = true
			continue
		case block && line == ")":
			block = false
			continue
		case strings.HasPrefix(line, "require "):
			line = strings.TrimPrefix(line, "require ")
		case !block:
			continue
		}
		if f := strings.Fields(line); len(f) >= 2 && !strings.HasSuffix(line, "// indirect") {
			out = append(out, f[0])
		}
	}
	return slices.Sorted(slices.Values(out))
}

func TestNestedModules_requireTheLatestRelease(t *testing.T) {
	t.Parallel()
	changelog, err := os.ReadFile("CHANGELOG.md")
	require.NoError(t, err)
	var checked int
	for _, dir := range []string{"vtbrowser", filepath.Join("internal", "site")} {
		gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		require.NoError(t, err)
		if !strings.HasPrefix(string(gomod), "module "+viaPkg+"/") {
			continue
		}
		checked++
		assert.Empty(t, releaseDrift(string(changelog), string(gomod)), "%s/go.mod", dir)
	}
	require.NotZero(t, checked, "expected a nested module under %s", viaPkg)
}

func TestReleaseLint_flagsANestedRequireBehindTheChangelog(t *testing.T) {
	t.Parallel()
	changelog := "# Changelog\n\n## Unreleased\n\n## v0.9.2 — x\n\n## v0.9.1\n"
	assert.Empty(t, releaseDrift(changelog, "module m\n\nrequire github.com/go-via/via v0.9.2\n"))
	assert.Equal(t, "requires via v0.9.1, but CHANGELOG.md's latest release is v0.9.2: "+
		"a release tags every module on one commit, so its nested go.mod files require that version",
		releaseDrift(changelog, "require (\n\tgithub.com/go-via/via v0.9.1\n)\n\nreplace github.com/go-via/via => ../"))
}

var (
	releaseHeading = regexp.MustCompile(`(?m)^## (v\d+\.\d+\.\d+)\b`)
	viaRequire     = regexp.MustCompile(`(?m)^\s*(?:require\s+)?github\.com/go-via/via (v\S+)`)
)

func releaseDrift(changelog, gomod string) string {
	latest, req := releaseHeading.FindStringSubmatch(changelog), viaRequire.FindStringSubmatch(gomod)
	if latest == nil || req == nil || latest[1] == req[1] {
		return ""
	}
	return "requires via " + req[1] + ", but CHANGELOG.md's latest release is " + latest[1] +
		": a release tags every module on one commit, so its nested go.mod files require that version"
}

func TestSources_holdNoDeadCode(t *testing.T) {
	t.Parallel()
	// Three deadcode runs hold ~2 GiB together, over gomutants' per-mutant
	// memory cap: every mutant re-checked against the full suite would read
	// TIMED OUT (gomutants/gomutants#124). mutation.sh passes -short; ci.sh
	// does not, so the lint still gates there.
	if testing.Short() {
		t.Skip("deadcode is a ci.sh lint, not a mutation killer")
	}
	out, err := exec.Command("go", "tool", "-n", "deadcode").Output()
	require.NoError(t, err)
	bin := strings.TrimSpace(string(out))
	for _, m := range []struct {
		dir  string
		tags []string
	}{{".", nil}, {"vtbrowser", []string{"-tags", "browser"}}, {filepath.Join("internal", "site"), nil}} {
		t.Run(m.dir, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(bin, append(append([]string{"-test", "-json"}, m.tags...), "./...")...)
			cmd.Dir = m.dir
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			require.NoError(t, err, "deadcode in %s: %s", m.dir, stderr.String())
			var pkgs []struct{ Funcs []deadFunc }
			require.NoError(t, json.Unmarshal(out, &pkgs))
			var funcs []deadFunc
			for _, p := range pkgs {
				funcs = append(funcs, p.Funcs...)
			}
			got, err := unexplainedDead(m.dir, funcs)
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

type deadFunc struct {
	Name     string
	Position struct {
		File string
		Line int
	}
}

func TestDeadCodeLint_keepsVocabularyMarkersAndShownSnippets(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"p/p.go": `package p

func Abbr(kids ...any) any { return el("abbr", kids...) }

func Stale() int { return 1 }

type slot struct{}

func (slot) isMarker() {}`,
		"snippet/src/x/x.go": "package x\n\nfunc Shown() {}",
	})
	dead := func(name, file string, line int) deadFunc {
		var f deadFunc
		f.Name, f.Position.File, f.Position.Line = name, file, line
		return f
	}
	got, err := unexplainedDead(root, []deadFunc{
		dead("Abbr", "p/p.go", 3), dead("Stale", "p/p.go", 5), dead("slot.isMarker", "p/p.go", 9),
		dead("Shown", "snippet/src/x/x.go", 3),
	})
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(root, "p", "p.go") +
		":5: Stale is unreachable, even from tests: delete it, or test it if it is API"}, got)
}

// unexplainedDead drops the funcs deadcode cannot see the use of: a vocabulary
// entry (see isVocabulary) is API for callers, an empty method marks a type
// for an interface or reflect, and snippet/src is code the site shows rather
// than runs.
func unexplainedDead(dir string, funcs []deadFunc) ([]string, error) {
	parsed := map[string]parsedFile{}
	var out []string
	for _, fn := range funcs {
		file := fn.Position.File
		if strings.Contains(filepath.ToSlash(file), "snippet/src/") {
			continue
		}
		f, ok := parsed[file]
		if !ok {
			fset := token.NewFileSet()
			af, err := parser.ParseFile(fset, filepath.Join(dir, file), nil, parser.SkipObjectResolution)
			if err != nil {
				return nil, err
			}
			f = parsedFile{file, fset, af}
			parsed[file] = f
		}
		if fd := funcAt(f, fn.Position.Line); fd != nil && (isVocabulary(fd) || fd.Recv != nil && len(fd.Body.List) == 0) {
			continue
		}
		out = append(out, fmt.Sprintf("%s:%d: %s is unreachable, even from tests: delete it, or test it if it is API",
			filepath.Join(dir, file), fn.Position.Line, fn.Name))
	}
	return out, nil
}

func funcAt(f parsedFile, line int) *ast.FuncDecl {
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil && f.fset.Position(fd.Name.Pos()).Line == line {
			return fd
		}
	}
	return nil
}

// apiPins is the exported API of each importable package, one declaration
// per line as apiOf renders it. A change here is a change users see.
var apiPins = map[string]string{
	".": `
const ReasonBadRequest Reason
const ReasonForbidden Reason
const ReasonGone Reason
const ReasonInternal Reason
const ReasonMethodNotAllowed Reason
const ReasonNotFound Reason
const ReasonTooLarge Reason
const ReasonUnavailable Reason
func (*Ctx) Context() context.Context
func (*Ctx) Listen[T any](*topic.Topic[T], func(*Ctx, T))
func (*Ctx) OnConnect(func())
func (*Ctx) OnDispose(func())
func (*Ctx) Param[T any](string) T
func (*Ctx) Redirect(string)
func (*Ctx) RedirectExternal(string)
func (*Ctx) Request() *http.Request
func (*Ctx) Session() *Session
func (*Ctx) Tick(time.Duration, func(*Ctx))
func (*List[E]) Append(E)
func (*List[E]) Each(func(E) h.H) h.H
func (*List[E]) Remove(int)
func (*MemorySessionStore) Delete(context.Context, string) error
func (*MemorySessionStore) Load(context.Context, string) ([]byte, bool, error)
func (*MemorySessionStore) LoadVersion(context.Context, string) ([]byte, uint64, bool, error)
func (*MemorySessionStore) Save(context.Context, string, []byte, time.Duration) error
func (*MemorySessionStore) SaveIf(context.Context, string, []byte, time.Duration, uint64) (bool, error)
func (*Router) Close()
func (*Router) ServeHTTP(http.ResponseWriter, *http.Request)
func (*Router) Shutdown(context.Context) error
func (*Session) Delete()
func (*Session) Ensure() string
func (*Session) Get[T any]() (T, bool)
func (*Session) ID() string
func (*Session) Put(any)
func (*Session) Rotate() string
func (*SignalCS[T]) Bind() h.Attr
func (*SignalCS[T]) Display() h.H
func (*SignalCS[T]) Ref() expr.Expr
func (*Signal[T]) Bind() h.Attr
func (*Signal[T]) Display() h.H
func (*Signal[T]) Get() T
func (*Signal[T]) Ref() expr.Expr
func (*Signal[T]) Set(T)
func (*State[T]) Display() h.H
func (*State[T]) Get() T
func (*State[T]) Set(T)
func (*State[T]) Track(*Ctx, *topic.Topic[T], func() T)
func Child[C any](C) h.H
func Each[T any]([]T, func(T) h.H) h.H
func Gzip(int) Encoder
func Handler[T any, PT ptrViewer[T]](T, ...Option) *Router
func ListOf[E any](...E) List[E]
func Mount[T any, PT ptrViewer[T]](*Router, string, T, ...MountOption)
func NewMemorySessionStore() *MemorySessionStore
func NewRouter(...Option) *Router
func PostForm(func(*Ctx), ...h.H) h.H
func StateOf[T any](T) State[T]
func StateTrack[T any](*topic.Topic[T], func() T) State[T]
func When(bool, func() h.H) h.H
func WithErrorPage(func(*Ctx, PageError) h.H) Option
func WithHead(Head) Option
func WithLogger(*slog.Logger) Option
func WithMaxBody(int64) Option
func WithMaxSSEConn(int) Option
func WithMaxUpload(int64) Option
func WithPageCompression(...Encoder) Option
func WithPinnedDeadline(time.Duration) Option
func WithSSECompression(...Encoder) Option
func WithSecureCookies() Option
func WithSessionCookieName(string) Option
func WithSessionKey([]byte) Option
func WithSessionStore(SessionStore) Option
func WithSessionStoreTimeout(time.Duration) Option
func WithSessionTTL(time.Duration) Option
func WithTrustedOrigin(string) Option
func WithUnsafeEval() Option
type Assets struct{Scripts []Script; Styles []Style; Preload []Preload; FontOrigins []string}
type Attr struct{Name string; Value string}
type Compressor interface{io.Writer; Flush() error; Close() error}
type Ctx struct{}
type Encoder struct{Token string; NewWriter func(io.Writer) Compressor}
type Head struct{Lang string; HTMLAttrs []Attr; BodyAttrs []Attr; Raw string; Assets Assets}
type List[E any] struct{State[[]E]}
type MemorySessionStore struct{}
type Meta struct{Title string; Description string; Canonical string; Robots string; OG map[string]string; Twitter map[string]string; Assets Assets; Lang string; HTMLAttrs []Attr; BodyAttrs []Attr}
type MountOption interface{…}
type Option interface{…}
type PageError struct{Status int; Reason Reason; Detail string; Err error}
type Preload struct{Href string; As string}
type Reason string
type Router struct{}
type Script struct{Src string; Inline string; Module bool; Defer bool}
type Session struct{}
type SessionStore interface{Load(context.Context, string) ([]byte, bool, error); Save(context.Context, string, []byte, time.Duration) error; Delete(context.Context, string) error}
type SignalCS[T any] struct{}
type Signal[T any] struct{}
type State[T any] struct{}
type Style struct{Href string; Inline string}
type VersionedSessionStore interface{SessionStore; LoadVersion(context.Context, string) ([]byte, uint64, bool, error); SaveIf(context.Context, string, []byte, time.Duration, uint64) (bool, error)}
var ErrForbidden
var ErrNotFound
var ErrStaleTab
var ErrStoreDown
`,
	"expr": `
const El Expr
func (Expr) Add(any) Expr
func (Expr) Assign(any) Expr
func (Expr) Eq(any) Expr
func (Expr) Ge(any) Expr
func (Expr) Gt(any) Expr
func (Expr) Le(any) Expr
func (Expr) Lt(any) Expr
func (Expr) Ne(any) Expr
func (Expr) Not() Expr
func (Expr) String() string
func (Expr) Toggle() Expr
func All(...Expr) Expr
func Any(...Expr) Expr
func Call(string, ...Expr) Expr
func Class(string, bool) Expr
func CopyTextOf(string) Expr
func CopyToClipboard(Expr) Expr
func Do(...Expr) Expr
func Raw(string) Expr
func Rawf(string, ...Expr) Expr
func Val(any) Expr
type Expr string
`,
	"h": `
func A(...H) H
func Abbr(...H) H
func Accept(string) Attr
func Action(string) Attr
func Address(...H) H
func Alt(string) Attr
func Area(...H) H
func Aria(string, string) Attr
func Article(...H) H
func Aside(...H) H
func Async(bool) Attr
func Audio(...H) H
func AutoComplete(string) Attr
func AutoFocus(bool) Attr
func B(...H) H
func Bdi(...H) H
func Bdo(...H) H
func Blockquote(...H) H
func Br(...H) H
func Button(...H) H
func Canvas(...H) H
func Caption(...H) H
func Checked(bool) Attr
func Cite(...H) H
func Class(...string) Attr
func Code(...H) H
func Col(...H) H
func ColSpan(int) Attr
func Colgroup(...H) H
func Cols(int) Attr
func Controls(bool) Attr
func Data(string, string) Attr
func DataAttr[V ~string](string, V) Attr
func DataClass[V ~string](string, V) Attr
func DataComputed[V ~string](string, V) Attr
func DataEffect[V ~string](...V) Attr
func DataIgnoreMorph() Attr
func DataIndicator[V ~string](V) Attr
func DataOn[V ~string](string, ...V) Attr
func DataRef[V ~string](V) Attr
func DataShow[V ~string](V) Attr
func DataStyle[V ~string](string, V) Attr
func DataText[V ~string](V) Attr
func Datalist(...H) H
func Dd(...H) H
func Defer(bool) Attr
func Del(...H) H
func Details(...H) H
func Dfn(...H) H
func Dialog(...H) H
func Disabled(bool) Attr
func Div(...H) H
func Dl(...H) H
func Dt(...H) H
func El(string, ...H) H
func Em(...H) H
func Embed(...H) H
func Enctype(string) Attr
func Fieldset(...H) H
func Figcaption(...H) H
func Figure(...H) H
func Footer(...H) H
func For(string) Attr
func Form(...H) H
func H1(...H) H
func H2(...H) H
func H3(...H) H
func H4(...H) H
func H5(...H) H
func H6(...H) H
func Header(...H) H
func Height[T Stringish](T) Attr
func Hgroup(...H) H
func Hidden(bool) Attr
func Hr(...H) H
func Href(string) Attr
func I(...H) H
func ID(string) Attr
func Iframe(...H) H
func Img(...H) H
func Inert(bool) Attr
func Input(...H) H
func Ins(...H) H
func Kbd(...H) H
func Label(...H) H
func Lang(string) Attr
func Legend(...H) H
func Li(...H) H
func Loop(bool) Attr
func Main(...H) H
func Map(...H) H
func Mark(...H) H
func MaxLength(int) Attr
func Max[T Stringish](T) Attr
func Menu(...H) H
func Meter(...H) H
func Method(string) Attr
func MinLength(int) Attr
func Min[T Stringish](T) Attr
func Multiple(bool) Attr
func Muted(bool) Attr
func Name(string) Attr
func Nav(...H) H
func NoValidate(bool) Attr
func Noscript(...H) H
func Object(...H) H
func Ol(...H) H
func Open(bool) Attr
func Optgroup(...H) H
func Option(...H) H
func Output(...H) H
func P(...H) H
func Picture(...H) H
func Placeholder(string) Attr
func PlaysInline(bool) Attr
func Pre(...H) H
func Progress(...H) H
func Q(...H) H
func RawAttr(string, string) Attr
func ReadOnly(bool) Attr
func Rel(string) Attr
func Required(bool) Attr
func Reversed(bool) Attr
func Role(string) Attr
func RowSpan(int) Attr
func Rows(int) Attr
func Rp(...H) H
func Rt(...H) H
func Ruby(...H) H
func S(...H) H
func Samp(...H) H
func Search(...H) H
func Section(...H) H
func Select(...H) H
func Selected(bool) Attr
func Small(...H) H
func Source(...H) H
func Span(...H) H
func Src(string) Attr
func Step[T Stringish](T) Attr
func Str[T Stringish](T) H
func Strong(...H) H
func Style(string) Attr
func Sub(...H) H
func Summary(...H) H
func Sup(...H) H
func TabIndex(int) Attr
func Table(...H) H
func Target(string) Attr
func Tbody(...H) H
func Td(...H) H
func Textarea(...H) H
func Tfoot(...H) H
func Th(...H) H
func Thead(...H) H
func Time(...H) H
func Title(string) Attr
func Tr(...H) H
func Track(...H) H
func Type(string) Attr
func U(...H) H
func Ul(...H) H
func Value[T Stringish](T) Attr
func Var(...H) H
func Video(...H) H
func Wbr(...H) H
func Width[T Stringish](T) Attr
type Attr = render.Attr
type H = render.H
type Stringish = render.Stringish
`,
	"on": `
func Bind[T any](func(*via.Ctx, T), T) Bound
func BlurCS(expr.Expr, ...Option) h.Attr
func Blur[F func(*via.Ctx) | Bound](F, ...Option) h.Attr
func ChangeCS(expr.Expr, ...Option) h.Attr
func Change[F func(*via.Ctx) | Bound](F, ...Option) h.Attr
func ClickCS(expr.Expr, ...Option) h.Attr
func Click[F func(*via.Ctx) | Bound](F, ...Option) h.Attr
func DblClickCS(expr.Expr, ...Option) h.Attr
func DblClick[F func(*via.Ctx) | Bound](F, ...Option) h.Attr
func EventCS(string, expr.Expr, ...Option) h.Attr
func Event[F func(*via.Ctx) | Bound](string, F, ...Option) h.Attr
func FocusCS(expr.Expr, ...Option) h.Attr
func Focus[F func(*via.Ctx) | Bound](F, ...Option) h.Attr
func InputCS(expr.Expr, ...Option) h.Attr
func Input[F func(*via.Ctx) | Bound](F, ...Option) h.Attr
func KeydownCS(expr.Expr, ...Option) h.Attr
func Keydown[F func(*via.Ctx) | Bound](F, ...Option) h.Attr
func KeyupCS(expr.Expr, ...Option) h.Attr
func Keyup[F func(*via.Ctx) | Bound](F, ...Option) h.Attr
func LoadCS(expr.Expr, ...Option) h.Attr
func Load[F func(*via.Ctx) | Bound](F, ...Option) h.Attr
func MouseEnterCS(expr.Expr, ...Option) h.Attr
func MouseEnter[F func(*via.Ctx) | Bound](F, ...Option) h.Attr
func MouseLeaveCS(expr.Expr, ...Option) h.Attr
func MouseLeave[F func(*via.Ctx) | Bound](F, ...Option) h.Attr
func ScrollCS(expr.Expr, ...Option) h.Attr
func Scroll[F func(*via.Ctx) | Bound](F, ...Option) h.Attr
func SubmitCS(expr.Expr, ...Option) h.Attr
func Submit[F func(*via.Ctx) | Bound](F, ...Option) h.Attr
func WithDebounce(time.Duration) Option
func WithModifier(string) Option
func WithOnce() Option
func WithOutside() Option
func WithPrevent() Option
func WithStop() Option
func WithThrottle(time.Duration) Option
func WithWindow() Option
type Bound struct{}
type Option interface{…}
`,
	"topic": `
const DefaultLimit
func (*Sub[T]) Drain() ([]T, bool)
func (*Sub[T]) Dropped() int
func (*Sub[T]) Ready() <-chan struct{}
func (*Sub[T]) Stop()
func (*Sub[T]) WakeOn(chan<- struct{})
func (*Topic[T]) NumSubs() int
func (*Topic[T]) Publish(T)
func (*Topic[T]) Subscribe() *Sub[T]
func (*Topic[T]) SubscribeLimit(int) *Sub[T]
func New[T any]() *Topic[T]
type Sub[T any] struct{}
type Topic[T any] struct{}
`,
	"vt": `
func (*Action) Body(string) *Action
func (*Action) Fire() (int, string)
func (*Action) Header(string, string) *Action
func (*Action) Host(string) *Action
func (*Action) NoOrigin() *Action
func (*Action) Origin(string) *Action
func (*Action) Over(*Conn) *Action
func (*Action) Raw(string) *Action
func (*Action) SecFetch(string) *Action
func (*Action) Tab(string) *Action
func (*App) Action(int) *Action
func (*App) ChildAction(string, int) *Action
func (*App) Client() *http.Client
func (*App) Connect() *Conn
func (*App) ConnectAt(string, string) *Conn
func (*App) ConnectWith(string) *Conn
func (*App) Get(string) (int, string)
func (*App) URL() string
func (*Conn) ActionURL(string, int) string
func (*Conn) Await(string) string
func (*Conn) AwaitClose() error
func (*Conn) Close()
func (*Conn) Peek() (string, bool)
func (*Conn) TabID() string
func Logger(testing.TB) *slog.Logger
func Serve(testing.TB, http.Handler) *App
func ServeTLS(testing.TB, http.Handler) *App
type Action struct{}
type App struct{}
type Conn struct{}
`,
	"vtbrowser": `
func (*Session) Click(string)
func (*Session) ConsoleErrors() []string
func (*Session) Eval(string, any)
func (*Session) GoTo(string)
func (*Session) NewTab() *Session
func (*Session) Reload()
func (*Session) RequireCleanConsole()
func (*Session) Restart(time.Duration, http.Handler)
func (*Session) SetViewport(int, int)
func (*Session) Sleep(time.Duration)
func (*Session) Text(string) string
func (*Session) Type(string, string)
func (*Session) Value(string) string
func (*Session) WaitBoundSignal(string, string)
func (*Session) WaitEvalTrue(string, string)
func (*Session) WaitFor(string, func(text string) bool, string)
func (*Session) WaitLiveConnected()
func (*Session) WaitLoaded()
func (*Session) WaitTextContains(string, string)
func (*Session) WaitValue(string, string)
func Open(testing.TB, http.Handler) *Session
func OpenURL(testing.TB, string) *Session
type Session struct{}
`,
}

func TestPublicAPI_matchesThePins(t *testing.T) {
	t.Parallel()
	files, err := publicFiles(".")
	require.NoError(t, err)
	dirs := map[string]bool{}
	for _, f := range files {
		dirs[filepath.Dir(f.rel)] = true
	}
	assert.ElementsMatch(t, slices.Collect(maps.Keys(dirs)), slices.Collect(maps.Keys(apiPins)),
		"every importable package has an apiPins entry")
	for dir, want := range apiPins {
		got, err := apiOf(dir)
		require.NoError(t, err)
		assert.Equal(t, strings.TrimSpace(want), strings.Join(got, "\n"),
			"the public API of %s changed: if you meant it, update apiPins and say so in CHANGELOG.md", dir)
	}
}

func TestAPILint_rendersEachExportedDeclaration(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{"p.go": `package p

import "io"

type Handle[T any] struct {
	id  string
	Val T
	io.Reader
}

type Option interface{ apply(*int) }

type Viewer interface {
	View(name string) string
	hidden()
}

type H = io.Writer

type Fn func(n int) error

const Max, min = 5, 1

var ErrGone error

func New[T any](name string, a, b int) (*Handle[T], error) { return nil, nil }

func (h *Handle[T]) Get() T { var z T; return z }

func (h Handle[T]) set(T) {}

func helper() {}`, "p_test.go": "package p_test\n\nfunc Exported() {}"})
	got, err := apiOf(root)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"const Max",
		"func (*Handle[T]) Get() T",
		"func New[T any](string, int, int) (*Handle[T], error)",
		"type Fn func(int) error",
		"type H = io.Writer",
		"type Handle[T any] struct{Val T; io.Reader}",
		"type Option interface{…}",
		"type Viewer interface{View(string) string; …}",
		"var ErrGone error",
	}, got)
}

// apiOf renders each exported declaration in dir on one line, param names
// dropped so a rename inside a signature is not an API change. An interface
// with unexported methods ends in "…": no other package can implement it.
func apiOf(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, d := range f.Decls {
			out = append(out, apiLines(fset, d)...)
		}
	}
	return slices.Sorted(slices.Values(out)), nil
}

func apiLines(fset *token.FileSet, d ast.Decl) []string {
	r := apiRenderer{fset}
	switch d := d.(type) {
	case *ast.FuncDecl:
		if !exportedFunc(d) {
			return nil
		}
		recv := ""
		if d.Recv != nil {
			recv = "(" + r.src(d.Recv.List[0].Type) + ") "
		}
		return []string{"func " + recv + d.Name.Name + r.signature(d.Type)}
	case *ast.GenDecl:
		var out []string
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				if !s.Name.IsExported() {
					continue
				}
				head := "type " + s.Name.Name
				if s.TypeParams != nil {
					head += strings.TrimSuffix(r.signature(&ast.FuncType{TypeParams: s.TypeParams, Params: &ast.FieldList{}}), "()")
				}
				if s.Assign.IsValid() {
					head += " ="
				}
				out = append(out, head+" "+r.typ(s.Type))
			case *ast.ValueSpec:
				for _, name := range s.Names {
					if !name.IsExported() {
						continue
					}
					line := d.Tok.String() + " " + name.Name
					if s.Type != nil {
						line += " " + r.typ(s.Type)
					}
					out = append(out, line)
				}
			}
		}
		return out
	}
	return nil
}

type apiRenderer struct{ fset *token.FileSet }

func (r apiRenderer) src(n ast.Node) string {
	var b strings.Builder
	if err := printer.Fprint(&b, r.fset, n); err != nil {
		return "?"
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// signature renders a func type after the word func, param names dropped.
func (r apiRenderer) signature(ft *ast.FuncType) string {
	unnamed := func(fl *ast.FieldList) *ast.FieldList {
		if fl == nil {
			return nil
		}
		out := &ast.FieldList{}
		for _, f := range fl.List {
			for range max(len(f.Names), 1) {
				out.List = append(out.List, &ast.Field{Type: f.Type})
			}
		}
		return out
	}
	return strings.TrimPrefix(r.src(&ast.FuncType{TypeParams: ft.TypeParams, Params: unnamed(ft.Params), Results: unnamed(ft.Results)}), "func")
}

func (r apiRenderer) typ(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.FuncType:
		return "func" + r.signature(t)
	case *ast.StructType:
		var fields []string
		for _, f := range t.Fields.List {
			if len(f.Names) == 0 && ast.IsExported(recvName(f.Type)) {
				fields = append(fields, r.src(f.Type))
			}
			for _, n := range f.Names {
				if n.IsExported() {
					fields = append(fields, n.Name+" "+r.typ(f.Type))
				}
			}
		}
		return "struct{" + strings.Join(fields, "; ") + "}"
	case *ast.InterfaceType:
		var methods []string
		sealed := false
		for _, m := range t.Methods.List {
			switch {
			case len(m.Names) == 0:
				methods = append(methods, r.src(m.Type))
			case !m.Names[0].IsExported():
				sealed = true
			default:
				methods = append(methods, m.Names[0].Name+r.signature(m.Type.(*ast.FuncType)))
			}
		}
		if sealed {
			methods = append(methods, "…")
		}
		return "interface{" + strings.Join(methods, "; ") + "}"
	}
	return r.src(e)
}
