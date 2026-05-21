package via

// SliceOps is the chain returned by Op(ctx) on every Slice* reactive
// type. Embeds Ops[[]T] for Apply/To plus the slice verbs.
type SliceOps[T any] struct {
	Ops[[]T]
}

// Append adds v to the end.
func (o *SliceOps[T]) Append(v T) {
	o.Apply(func(cur []T) []T { return append(cur, v) })
}

// Prepend adds v to the front. Allocates a new slice — needed because
// in-place prepend isn't possible without reallocating.
func (o *SliceOps[T]) Prepend(v T) {
	o.Apply(func(cur []T) []T {
		out := make([]T, 0, len(cur)+1)
		out = append(out, v)
		out = append(out, cur...)
		return out
	})
}

// Pop removes the last element. No-op on empty.
func (o *SliceOps[T]) Pop() {
	o.Apply(func(cur []T) []T {
		if len(cur) == 0 {
			return cur
		}
		return cur[:len(cur)-1]
	})
}

// Shift removes the first element. No-op on empty.
func (o *SliceOps[T]) Shift() {
	o.Apply(func(cur []T) []T {
		if len(cur) == 0 {
			return cur
		}
		return cur[1:]
	})
}

// Empty replaces the value with nil (zero-length slice).
func (o *SliceOps[T]) Empty() { o.To(nil) }

// Take keeps the first n elements. n <= 0 clears; n >= len is a no-op.
func (o *SliceOps[T]) Take(n int) {
	o.Apply(func(cur []T) []T {
		if n <= 0 {
			return nil
		}
		if n >= len(cur) {
			return cur
		}
		return cur[:n]
	})
}

// Drop discards the first n elements. n <= 0 is a no-op; n >= len
// clears.
func (o *SliceOps[T]) Drop(n int) {
	o.Apply(func(cur []T) []T {
		if n <= 0 {
			return cur
		}
		if n >= len(cur) {
			return nil
		}
		return cur[n:]
	})
}

// Filter keeps only elements for which pred returns true. Allocates a
// new slice so the result doesn't alias the input. Nil pred is a no-op.
func (o *SliceOps[T]) Filter(pred func(T) bool) {
	if pred == nil {
		return
	}
	o.Apply(func(cur []T) []T {
		out := make([]T, 0, len(cur))
		for _, v := range cur {
			if pred(v) {
				out = append(out, v)
			}
		}
		return out
	})
}

// SignalSlice is the slice-specialized Signal.
type SignalSlice[T any] struct{ Signal[[]T] }

// Op returns a slice chain bound to ctx.
func (s *SignalSlice[T]) Op(ctx *Ctx) *SliceOps[T] {
	return &SliceOps[T]{Ops: Ops[[]T]{apply: func(fn func([]T) []T) { s.Update(ctx, fn) }}}
}

// StateTabSlice is the slice-specialized StateTab.
type StateTabSlice[T any] struct{ StateTab[[]T] }

// Op returns a slice chain bound to ctx.
func (s *StateTabSlice[T]) Op(ctx *Ctx) *SliceOps[T] {
	return &SliceOps[T]{Ops: Ops[[]T]{apply: func(fn func([]T) []T) { s.Update(ctx, fn) }}}
}

// StateSessSlice is the slice-specialized StateSess.
type StateSessSlice[T any] struct{ StateSess[[]T] }

// Op returns a slice chain bound to ctx.
func (s *StateSessSlice[T]) Op(ctx *Ctx) *SliceOps[T] {
	return &SliceOps[T]{Ops: Ops[[]T]{apply: func(fn func([]T) []T) { s.Update(ctx, fn) }}}
}

// StateAppSlice is the slice-specialized StateApp.
type StateAppSlice[T any] struct{ StateApp[[]T] }

// Op returns a slice chain bound to ctx.
func (a *StateAppSlice[T]) Op(ctx *Ctx) *SliceOps[T] {
	return &SliceOps[T]{Ops: Ops[[]T]{apply: func(fn func([]T) []T) { a.Update(ctx, fn) }}}
}
