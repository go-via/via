package via

// Action constrains the bound-method shapes the via/on helpers accept:
// func(*Ctx) error, or func(*Ctx) when nothing in the body can fail.
// It is a type-parameter constraint (a union), so passing anything else
// to on.Click and friends is a compile error rather than a runtime
// panic. The value must still be a bound method value (e.g. c.Inc) —
// closures and top-level functions satisfy the type but have no method
// name to route to, and panic at first render.
type Action interface {
	func(*Ctx) | func(*Ctx) error
}
