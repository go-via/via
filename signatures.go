package via

import (
	"fmt"
	"reflect"
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
	ctxPtrType = reflect.TypeOf((*Ctx)(nil))
	errorType  = reflect.TypeOf((*error)(nil)).Elem()
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
				"  expected: "+want.repr+"\n",
			typ.String(), name, name))
	}
	return m.Index
}

func checkViewSignature(typ reflect.Type, m reflect.Method) {
	mt := m.Type
	if mt.NumIn() != 2 || mt.NumOut() != 1 ||
		mt.In(1) != ctxPtrType {
		panic(fmt.Sprintf(
			"via.Mount(%s): View has the wrong signature\n"+
				"\n"+
				"  expected: func (c *%s) View(ctx *via.Ctx) h.H\n",
			typ.String(), typ.Name()))
	}
	// View's return type is checked structurally — h.H is an interface so
	// the concrete return type can be anything assignable to it; we trust
	// the assertion at render time.
}

// actionMethodKind reports whether m is a valid action method and its
// return shape. Recognised signatures:
//
//	func (c *T) Inc(ctx *via.Ctx) error  // void=false
//	func (c *T) Inc(ctx *via.Ctx)        // void=true (no return)
//
// Lifecycle method names are excluded so they don't masquerade as
// actions when their signature happens to match.
func actionMethodKind(m reflect.Method) (void bool, ok bool) {
	mt := m.Type
	if mt.NumIn() != 2 {
		return false, false
	}
	if mt.In(1) != ctxPtrType {
		return false, false
	}
	switch m.Name {
	case "View", "OnInit", "OnConnect", "OnDispose":
		return false, false
	}
	switch mt.NumOut() {
	case 0:
		return true, true
	case 1:
		if mt.Out(0) != errorType {
			return false, false
		}
		return false, true
	}
	return false, false
}
