package via

// SliceOps is the chain returned by Op(ctx) on every Slice* reactive
// type; its slice verbs route through the handle's Update path.
type SliceOps[T any] struct {
	ops[[]T]
}

// Append adds v to the end.
func (o *SliceOps[T]) Append(v T) {
	_ = o.update(func(cur []T) ([]T, error) { return append(cur, v), nil })
}

// Prepend adds v to the front. Allocates a new slice — in-place
// prepend isn't possible without reallocating.
func (o *SliceOps[T]) Prepend(v T) {
	_ = o.update(func(cur []T) ([]T, error) {
		out := make([]T, 0, len(cur)+1)
		out = append(out, v)
		out = append(out, cur...)
		return out, nil
	})
}

// Pop removes the last element. No-op on empty.
func (o *SliceOps[T]) Pop() {
	_ = o.update(func(cur []T) ([]T, error) {
		if len(cur) == 0 {
			return cur, nil
		}
		return cur[:len(cur)-1], nil
	})
}

// Shift removes the first element. No-op on empty.
func (o *SliceOps[T]) Shift() {
	_ = o.update(func(cur []T) ([]T, error) {
		if len(cur) == 0 {
			return cur, nil
		}
		return cur[1:], nil
	})
}

// Empty replaces the value with nil (zero-length slice).
func (o *SliceOps[T]) Empty() {
	_ = o.update(func([]T) ([]T, error) { return nil, nil })
}

// Take keeps the first n elements. n <= 0 clears; n >= len is a no-op.
func (o *SliceOps[T]) Take(n int) {
	_ = o.update(func(cur []T) ([]T, error) {
		if n <= 0 {
			return nil, nil
		}
		if n >= len(cur) {
			return cur, nil
		}
		return cur[:n], nil
	})
}

// Drop discards the first n elements. n <= 0 is a no-op; n >= len
// clears.
func (o *SliceOps[T]) Drop(n int) {
	_ = o.update(func(cur []T) ([]T, error) {
		if n <= 0 {
			return cur, nil
		}
		if n >= len(cur) {
			return nil, nil
		}
		return cur[n:], nil
	})
}

// Filter keeps only elements for which pred returns true. Allocates a
// new slice so the result doesn't alias the input. Nil pred is a no-op.
func (o *SliceOps[T]) Filter(pred func(T) bool) {
	if pred == nil {
		return
	}
	_ = o.update(func(cur []T) ([]T, error) {
		out := make([]T, 0, len(cur))
		for _, v := range cur {
			if pred(v) {
				out = append(out, v)
			}
		}
		return out, nil
	})
}

// SignalSlice is the slice-specialized Signal.
type SignalSlice[T any] struct{ Signal[[]T] }

// Op returns a slice chain bound to ctx.
func (s *SignalSlice[T]) Op(ctx *Ctx) *SliceOps[T] {
	mustOpCtx(ctx)
	return &SliceOps[T]{ops: ops[[]T]{update: func(fn func([]T) ([]T, error)) error { return s.Update(ctx, fn) }}}
}

// StateTabSlice is the slice-specialized StateTab.
type StateTabSlice[T any] struct{ StateTab[[]T] }

// Op returns a slice chain bound to ctx.
func (s *StateTabSlice[T]) Op(ctx *Ctx) *SliceOps[T] {
	mustOpCtx(ctx)
	return &SliceOps[T]{ops: ops[[]T]{update: func(fn func([]T) ([]T, error)) error { return s.Update(ctx, fn) }}}
}

// StateSessSlice is the slice-specialized StateSess.
type StateSessSlice[T any] struct{ StateSess[[]T] }

// Op returns a slice chain bound to ctx.
func (s *StateSessSlice[T]) Op(ctx *Ctx) *SliceOps[T] {
	mustOpCtx(ctx)
	return &SliceOps[T]{ops: ops[[]T]{update: func(fn func([]T) ([]T, error)) error { return s.Update(ctx, fn) }}}
}

// StateAppSlice is the slice-specialized StateApp.
type StateAppSlice[T any] struct{ StateApp[[]T] }

// Op returns a slice chain bound to ctx.
func (a *StateAppSlice[T]) Op(ctx *Ctx) *SliceOps[T] {
	mustOpCtx(ctx)
	return &SliceOps[T]{ops: ops[[]T]{update: func(fn func([]T) ([]T, error)) error { return a.Update(ctx, fn) }}}
}
