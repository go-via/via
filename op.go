package via

// Ops[T] is the typed chain entry returned by Op(ctx) on every reactive
// handle. It exposes the universal mutators (Apply for custom transforms,
// To for a constant replace) plus shape-specific verbs in Phase C
// (Add/Toggle/Append/…) that ship on specialized handle types.
//
// Apply and To are pure sugar over Update — the handle's atomicity,
// dirty-marking, and broadcast semantics flow through unchanged.
//
//	p.Count.Op(ctx).Apply(func(n int) int { return n + 1 })
//	p.Theme.Op(ctx).To("dark")
type Ops[T any] struct {
	apply func(func(T) T)
}

// Apply runs fn under the handle's Update path. Nil fn is a no-op,
// matching every reactive handle's Update guarantee.
func (o *Ops[T]) Apply(fn func(T) T) {
	if o == nil || o.apply == nil || fn == nil {
		return
	}
	o.apply(fn)
}

// To replaces the current value with v. Equivalent to Apply(func(T) T { return v }),
// surfaced as the canonical "write a constant" verb.
func (o *Ops[T]) To(v T) {
	if o == nil || o.apply == nil {
		return
	}
	o.apply(func(T) T { return v })
}
