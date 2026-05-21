package via

// Ops[T] is the typed chain entry returned by Op(ctx) on every reactive
// handle. The generic surface carries To(v) — replace with a constant —
// and shape-specialized embeddings (NumOps / BoolOps / StrOps / SliceOps
// / MapOps) add type-aware verbs that all flow through the handle's
// Update path.
//
// For custom transforms with optional error, call the handle's
// Update(ctx, fn) directly — the Op chain is for canned verbs.
//
//	p.Theme.Op(ctx).To("dark")
//	p.Count.Op(ctx).Inc()
//	p.Count.Update(ctx, func(n int) (int, error) {
//	    if n >= max { return 0, errBudget }
//	    return n + 1, nil
//	})
type Ops[T any] struct {
	update func(func(T) (T, error)) error
}

// To replaces the current value with v. Equivalent to
// Update(ctx, func(T) (T, error) { return v, nil }), surfaced as the
// canonical "write a constant" verb.
func (o *Ops[T]) To(v T) {
	if o == nil || o.update == nil {
		return
	}
	_ = o.update(func(T) (T, error) { return v, nil })
}
