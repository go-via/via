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
//   - no '&' and no closures at a via call site (the example lint)

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// viaCallNames are the via entry points whose arguments must be named method
// values or by-value compositions — never an address-of or a closure. Param
// (a *Ctx method, called as ctx.Param) cannot appear here: isViaCall only
// matches a package-qualified call (via.X), and Param is never spelled that
// way.
var viaCallNames = map[string]bool{
	"Handler": true, "Mount": true, "Child": true, "When": true, "Each": true,
	"On": true, "OnArg": true, "PostForm": true,
}

func TestExamples_takeNoAddressOfOrClosureAtViaCallSites(t *testing.T) {
	t.Parallel()
	files := exampleGoFiles(t)
	require.NotEmpty(t, files, "expected example sources to lint")

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, file, nil, 0)
			require.NoError(t, err)

			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isViaCall(call) {
					return true
				}
				for _, arg := range call.Args {
					switch a := arg.(type) {
					case *ast.FuncLit:
						assert.Failf(t, "closure at via call site",
							"%s: pass a named method value, not a func literal", fset.Position(a.Pos()))
					case *ast.UnaryExpr:
						if a.Op == token.AND {
							assert.Failf(t, "address-of at via call site",
								"%s: via takes compositions by value — drop the '&'", fset.Position(a.Pos()))
						}
					case *ast.CompositeLit:
						// via.Child(p.Chat) embeds the parent's field; a literal
						// would re-seed a fresh child on every render.
						if isViaCallNamed(call, "Child") {
							assert.Failf(t, "composite literal at via.Child call site",
								"%s: pass the parent's field (p.Chat), not a literal", fset.Position(a.Pos()))
						}
					}
				}
				return true
			})
		})
	}
}

func isViaCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "via" && viaCallNames[sel.Sel.Name]
}

func isViaCallNamed(call *ast.CallExpr, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "via" && sel.Sel.Name == name
}

func TestCore_importsNoReflectPackage(t *testing.T) {
	t.Parallel()
	// reflect is admitted in exactly three files and only on type-setup paths
	// that run once per composition type (Mount/Child) and are memoized: the
	// action-id func name, the field-name signal table, the child's parent
	// field lookup, and the hook-shape check. Nothing here may run per render — that is the invariant
	// this whitelist exists to keep honest.
	allowed := map[string][]string{
		"via.go": {"reflect.Array", "reflect.Map", "reflect.Pointer", "reflect.PointerTo",
			"reflect.Slice", "reflect.Struct", "reflect.StructField", "reflect.Type",
			"reflect.TypeOf", "reflect.ValueOf"},
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
	for _, dir := range []string{".", "h", "topic"} {
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

func exampleGoFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir("example", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			files = append(files, p)
		}
		return nil
	})
	require.NoError(t, err)
	return files
}
