package h

import (
	"strings"

	"github.com/go-via/via/internal/hcore"
)

// One typed attribute per Datastar plugin, key spelled once. [Data] covers
// what is missing here: a modifier suffix, a newer plugin. The expression type
// is left open (~string) so h does not depend on the package that builds them.

// DataShow shows the element while the expression is truthy.
func DataShow[V ~string](e V) Attr { return Data("show", string(e)) }

// DataText replaces the element's text with the expression's value.
func DataText[V ~string](e V) Attr { return Data("text", string(e)) }

// DataClass toggles the named class while the expression is truthy.
func DataClass[V ~string](name string, e V) Attr { return Data("class:"+name, string(e)) }

// DataAttr sets the named attribute from the expression.
//
//	h.Button(h.DataAttr("disabled", draft.Ref().Eq("")), …)
//
// A client-side disable is cosmetic — the handler still validates.
func DataAttr[V ~string](name string, e V) Attr { return Data("attr:"+name, string(e)) }

// DataStyle sets the named CSS property from the expression.
func DataStyle[V ~string](prop string, e V) Attr { return Data("style:"+prop, string(e)) }

// DataOn runs the statements on the named DOM event. It writes the same
// attribute via.On does, so an element carrying both for one event emits
// data-on:<event> twice.
func DataOn[V ~string](event string, stmts ...V) Attr {
	return Data("on:"+event, joinStmts(stmts))
}

// DataEffect runs the statements whenever a signal they read changes.
func DataEffect[V ~string](stmts ...V) Attr { return Data("effect", joinStmts(stmts)) }

// DataComputed declares a read-only signal derived from the expression.
func DataComputed[V ~string](name string, e V) Attr { return Data("computed:"+name, string(e)) }

// DataIndicator names the signal Datastar holds true while a request from this
// element is in flight.
func DataIndicator[V ~string](sig V) Attr { return Data("indicator", signalName(sig)) }

// DataRef names the signal Datastar puts this element into.
func DataRef[V ~string](sig V) Attr { return Data("ref", signalName(sig)) }

// DataIgnoreMorph renders a bare data-ignore-morph. Datastar skips morphing a node
// only when the old and the new one both carry it, so put it on a container
// whose subtree some JS owns (a chart canvas, a map) and a live patch will
// leave that subtree alone.
func DataIgnoreMorph() Attr { return hcore.BoolAttr("data-ignore-morph", true) }

// A Ref() spells "$name"; indicator and ref want the bare name, and "$name"
// would declare a signal literally called "$name".
func signalName[V ~string](sig V) string { return strings.TrimPrefix(string(sig), "$") }

func joinStmts[V ~string](stmts []V) string {
	if len(stmts) == 0 {
		panic("h: a data-on or data-effect attribute needs at least one statement")
	}
	parts := make([]string, len(stmts))
	for i, s := range stmts {
		parts[i] = string(s)
	}
	return strings.Join(parts, "; ")
}
