package via

import (
	"fmt"
	"reflect"

	"github.com/go-via/via/h"
)

// expected method signatures for lifecycle hooks; Mount validates against
// these and panics with a helpful, format-the-fix-yourself message if a
// method exists but has the wrong shape.
//
// All lifecycle methods take exactly one *Ctx argument; only the return
// shape varies (error vs no return), so we only need to encode that.
type lifecycleSig struct {
	out    int    // number of outputs (0 for no-return, 1 for error)
	errOut bool   // true if output[0] must be error
	repr   string // human-readable form of the expected signature
}

var (
	sigErrReturn = lifecycleSig{out: 1, errOut: true, repr: "func (c *T) %s(ctx *via.Ctx) error"}
	sigVoid      = lifecycleSig{out: 0, errOut: false, repr: "func (c *T) %s(ctx *via.Ctx)"}

	// Cached reflect.Type values used by Mount-time signature checks.
	// reflect.TypeOf returns the same canonical type per call but each call
	// still allocates an interface header — cache once at package init.
	ctxPtrType  = reflect.TypeOf((*Ctx)(nil))
	ctxRPtrType = reflect.TypeOf((*CtxR)(nil))
	errorType   = reflect.TypeOf((*error)(nil)).Elem()
	hType       = reflect.TypeOf((*h.H)(nil)).Elem()
)

// checkAndIndexLifecycle validates the lifecycle method's signature and
// returns its method index, or -1 if the method doesn't exist on ptrTyp.
// Combines the signature check and the index lookup so callers don't
// have to call ptrTyp.MethodByName twice.
func checkAndIndexLifecycle(typ, ptrTyp reflect.Type, name string, want lifecycleSig) int {
	m, ok := ptrTyp.MethodByName(name)
	if !ok {
		return -1
	}
	mt := m.Type
	// && short-circuits: when NumOut != want.out (especially 0 vs 1)
	// the Out(0) call is skipped, so this is safe for the void case.
	bad := mt.NumIn() != 2 || mt.In(1) != ctxPtrType ||
		mt.NumOut() != want.out ||
		(want.errOut && mt.Out(0) != errorType)
	if bad {
		panic(fmt.Sprintf(
			"via.Mount(%s): %s has the wrong signature\n"+
				"\n"+
				"  expected: "+want.repr+"\n"+
				"       got: %s\n",
			typ.String(), name, name, mt.String()))
	}
	return m.Index
}

func checkViewSignature(typ reflect.Type, m reflect.Method) {
	mt := m.Type
	// Param shape: receiver + *CtxR (read-only render context), exactly
	// one return. Reject *via.Ctx so View can't accidentally hold the
	// mutator surface.
	badParams := mt.NumIn() != 2 || mt.NumOut() != 1 || mt.In(1) != ctxRPtrType
	// Return must be assignable to h.H.
	// Catches "View(ctx) int" at Mount time rather than at the first
	// request's view.Interface().(h.H) type-assert.
	badReturn := !badParams && !mt.Out(0).AssignableTo(hType)
	if badParams || badReturn {
		panic(fmt.Sprintf(
			"via.Mount(%s): View has the wrong signature\n"+
				"\n"+
				"  expected: func (c *%s) View(ctx *via.CtxR) h.H\n"+
				"       got: %s\n",
			typ.String(), typ.Name(), mt.String()))
	}
}

// actionMethodKind reports whether m is a valid action method and its
// return shape. Recognised signatures:
//
//	func (c *T) Inc(ctx *via.Ctx) error  // void=false
//	func (c *T) Inc(ctx *via.Ctx)        // void=true (no return)
//
// Lifecycle method names are excluded so they don't masquerade as
// actions when their signature happens to match.
//
// Panics if a method named like an action (one param, action-shaped
// return) takes *via.CtxR instead of *via.Ctx — the read-only context
// has no Set/Update, so this is always a user typo and silently
// dropping the method would make the missing-action mystery hard to
// debug.
func actionMethodKind(m reflect.Method) (void bool, ok bool) {
	mt := m.Type
	if mt.NumIn() != 2 {
		return false, false
	}
	switch m.Name {
	case "View", "OnInit", "OnConnect", "OnDispose":
		return false, false
	}
	// Detect action-shaped return early so the *CtxR diagnostic only
	// fires on methods the user clearly intended as actions.
	actionShape := mt.NumOut() == 0 ||
		(mt.NumOut() == 1 && mt.Out(0) == errorType)
	if !actionShape {
		return false, false
	}
	if mt.In(1) == ctxRPtrType {
		panic(fmt.Sprintf(
			"via.Mount: action %s takes *via.CtxR, but actions must take *via.Ctx\n"+
				"\n"+
				"  expected: func (c *T) %s(ctx *via.Ctx) error\n"+
				"       got: %s\n"+
				"\n"+
				"*via.CtxR is the read-only render context; it has no Set/Update,\n"+
				"so an action handler bound to it could not mutate state.\n",
			m.Name, m.Name, mt.String()))
	}
	if mt.In(1) != ctxPtrType {
		return false, false
	}
	if mt.NumOut() == 0 {
		return true, true
	}
	return false, true
}
