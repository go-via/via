package content

import "github.com/go-via/via/h"

//go:generate go run ./gen

// apiSymbol is one exported name of a via package, as go/doc read it. Name is
// "Type.Method" for a method.
type apiSymbol struct {
	pkg, name, kind, sig, doc, deprecated string
}

func (s apiSymbol) id() string { return s.pkg + "." + s.name }

var apiIndex = func() map[string]apiSymbol {
	m := make(map[string]apiSymbol, len(apiSymbols))
	for _, s := range apiSymbols {
		m[s.id()] = s
	}
	return m
}()

// API is sym as inline code linking to its /reference row. sym is the row id:
// package, then name, as in "via.WithTrustedOrigin", "expr.Val" or
// "via.State.Set".
//
//	h.P(h.Str("Set "), content.API("via.WithTrustedOrigin"), h.Str(" in production."))
//
// A name that is not in the generated API renders as plain code with no link,
// and a test fails on it: every API and APIText call must pass a string
// literal naming a symbol that exists.
func API(sym string) h.H { return apiLink(sym, sym) }

// APIText is API with a custom label, for prose that reads better with the
// bare name (APIText("expr.Val", "Val")) or with call syntax.
func APIText(sym, label string) h.H { return apiLink(sym, label) }

func apiLink(sym, label string) h.H {
	code := h.Code(h.Str(label))
	if _, ok := apiIndex[sym]; !ok {
		return code
	}
	// Relative, so the link stays inside a versioned build served under a
	// base path: every page sits one level below the base.
	return h.A(h.Href("reference#"+sym), code)
}
