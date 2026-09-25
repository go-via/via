package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const module = "github.com/go-via/via"

// packages is every public package of the module. vtbrowser is a module of its
// own but part of the documented testing surface; internal/ and the example
// apps under the root are not API.
var packages = []struct{ name, dir string }{
	{"via", "."},
	{"h", "h"},
	{"on", "on"},
	{"expr", "expr"},
	{"topic", "topic"},
	{"vt", "vt"},
	{"vtbrowser", "vtbrowser"},
}

type symbol struct {
	Pkg, Name, Kind, Sig, Doc, Deprecated string
}

func load(root string) ([]symbol, error) {
	var all []symbol
	for _, p := range packages {
		syms, err := loadPkg(filepath.Join(root, p.dir), p.name, path(p.dir))
		if err != nil {
			return nil, err
		}
		all = append(all, syms...)
	}
	return all, nil
}

func path(dir string) string {
	if dir == "." {
		return module
	}
	return module + "/" + dir
}

func loadPkg(dir, name, importPath string) ([]symbol, error) {
	bp, err := build.ImportDir(dir, 0)
	if err != nil {
		return nil, fmt.Errorf("gen: %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, f := range bp.GoFiles {
		af, err := parser.ParseFile(fset, filepath.Join(dir, f), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, af)
	}
	p, err := doc.NewFromFiles(fset, files, importPath)
	if err != nil {
		return nil, err
	}
	if p.Name != name {
		return nil, fmt.Errorf("gen: %s is package %s, want %s", dir, p.Name, name)
	}
	c := collector{fset: fset, pkg: p, name: name}
	c.values("const", p.Consts)
	c.values("var", p.Vars)
	c.funcs(p.Funcs)
	for _, t := range p.Types {
		c.add(t.Name, "type", typeSig(fset, t.Decl), t.Doc)
		c.values("const", t.Consts)
		c.values("var", t.Vars)
		c.funcs(t.Funcs)
		for _, m := range t.Methods {
			c.add(t.Name+"."+m.Name, "method", funcSig(fset, m.Decl), m.Doc)
		}
	}
	slices.SortFunc(c.out, func(a, b symbol) int { return strings.Compare(a.Name, b.Name) })
	return c.out, nil
}

type collector struct {
	fset *token.FileSet
	pkg  *doc.Package
	name string
	out  []symbol
}

func (c *collector) add(name, kind, sig, text string) {
	c.out = append(c.out, symbol{
		Pkg: c.name, Name: name, Kind: kind, Sig: sig,
		Doc: c.pkg.Synopsis(text), Deprecated: deprecated(text),
	})
}

func (c *collector) funcs(fs []*doc.Func) {
	for _, f := range fs {
		c.add(f.Name, "func", funcSig(c.fset, f.Decl), f.Doc)
	}
}

func (c *collector) values(kind string, vs []*doc.Value) {
	for _, v := range vs {
		for _, spec := range v.Decl.Specs {
			vs := spec.(*ast.ValueSpec)
			text := v.Doc
			if own := strings.TrimSpace(vs.Doc.Text() + vs.Comment.Text()); own != "" {
				text = own
			}
			for i, n := range vs.Names {
				if !n.IsExported() {
					continue
				}
				c.add(n.Name, kind, valueSig(c.fset, kind, vs, i), text)
			}
		}
	}
}

var (
	afterOpen   = regexp.MustCompile(`([(\[{])\s*\n\s*`)
	beforeClose = regexp.MustCompile(`,?\s*\n\s*([)\]}])`)
	newlines    = regexp.MustCompile(`\s*\n\s*`)
)

// oneLine joins what gofmt spread over several lines, dropping the trailing
// comma a multi-line parameter list needs.
func oneLine(fset *token.FileSet, node any) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, node); err != nil {
		panic(err)
	}
	s := afterOpen.ReplaceAllString(b.String(), "$1")
	s = beforeClose.ReplaceAllString(s, "$1")
	return newlines.ReplaceAllString(s, " ")
}

func funcSig(fset *token.FileSet, d *ast.FuncDecl) string {
	c := *d
	c.Body, c.Doc = nil, nil
	return oneLine(fset, &c)
}

// typeSig elides struct and interface members: the row is a pointer to the
// type, and pkg.go.dev one click away has every field.
func typeSig(fset *token.FileSet, d *ast.GenDecl) string {
	ts := *d.Specs[0].(*ast.TypeSpec)
	ts.Doc, ts.Comment = nil, nil
	switch t := ts.Type.(type) {
	case *ast.StructType:
		ts.Type = elided("struct", t.Incomplete || t.Fields.NumFields() > 0)
	case *ast.InterfaceType:
		ts.Type = elided("interface", t.Incomplete || t.Methods.NumFields() > 0)
	}
	return "type " + oneLine(fset, &ts)
}

// elided keeps "struct{}" for a truly empty type, so it is not mistaken for
// one whose fields are all unexported.
func elided(kw string, members bool) ast.Expr {
	if !members {
		return ast.NewIdent(kw + "{}")
	}
	return ast.NewIdent(kw + "{ … }")
}

func valueSig(fset *token.FileSet, kind string, vs *ast.ValueSpec, i int) string {
	s := kind + " " + vs.Names[i].Name
	if vs.Type != nil {
		s += " " + oneLine(fset, vs.Type)
	}
	if i < len(vs.Values) {
		s += " = " + oneLine(fset, vs.Values[i])
	}
	return s
}

// deprecated is the paragraph go/doc and pkg.go.dev treat as the deprecation
// notice, without its "Deprecated: " prefix.
func deprecated(text string) string {
	for _, para := range strings.Split(text, "\n\n") {
		if rest, ok := strings.CutPrefix(para, "Deprecated: "); ok {
			return strings.Join(strings.Fields(rest), " ")
		}
	}
	return ""
}

func render(syms []symbol) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// Code generated by go run ./gen; DO NOT EDIT.\n\npackage content\n\nvar apiSymbols = []apiSymbol{\n")
	for _, s := range syms {
		fmt.Fprintf(&b, "\t{%q, %q, %q, %q, %q, %q},\n", s.Pkg, s.Name, s.Kind, s.Sig, s.Doc, s.Deprecated)
	}
	b.WriteString("}\n")
	return format.Source(b.Bytes())
}
