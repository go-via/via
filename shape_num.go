package via

import "fmt"

// Number is the constraint for SignalNum / StateTabNum / StateSessNum /
// StateAppNum. Covers every Go-built-in integer and floating-point kind.
// Underlying-type approximation (~int etc.) lets users wrap these in
// named types (e.g. type UserID int) and still pick up the typed ops.
type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

// NumOps is the chain returned by Op(ctx) on every Num* reactive type;
// its numeric verbs route through the handle's Update path.
type NumOps[T Number] struct {
	ops[T]
}

// Add increments by v.
func (o *NumOps[T]) Add(v T) {
	_ = o.update(func(cur T) (T, error) { return cur + v, nil })
}

// Sub decrements by v.
func (o *NumOps[T]) Sub(v T) {
	_ = o.update(func(cur T) (T, error) { return cur - v, nil })
}

// Mul multiplies by v.
func (o *NumOps[T]) Mul(v T) {
	_ = o.update(func(cur T) (T, error) { return cur * v, nil })
}

// Div divides by v. Caller is responsible for non-zero v — division by
// zero panics for ints, yields NaN/Inf for floats per Go semantics.
func (o *NumOps[T]) Div(v T) {
	_ = o.update(func(cur T) (T, error) { return cur / v, nil })
}

// Inc adds 1.
func (o *NumOps[T]) Inc() { o.Add(1) }

// Dec subtracts 1.
func (o *NumOps[T]) Dec() { o.Sub(1) }

// Zero replaces the value with the type's zero.
func (o *NumOps[T]) Zero() {
	_ = o.update(func(T) (T, error) { var z T; return z, nil })
}

// AtLeast raises the value to lo when it is below; in-range values are
// untouched. After this call the value is at least lo.
func (o *NumOps[T]) AtLeast(lo T) {
	_ = o.update(func(cur T) (T, error) {
		if cur < lo {
			return lo, nil
		}
		return cur, nil
	})
}

// AtMost lowers the value to hi when it is above; in-range values are
// untouched. After this call the value is at most hi.
func (o *NumOps[T]) AtMost(hi T) {
	_ = o.update(func(cur T) (T, error) {
		if cur > hi {
			return hi, nil
		}
		return cur, nil
	})
}

// Clamp confines the value to [lo, hi]: values below lo are raised to
// lo, values above hi are lowered to hi, in-range values are untouched.
// Inverted bounds (lo > hi) are a programming mistake — Clamp panics
// rather than silently swapping or picking one bound.
func (o *NumOps[T]) Clamp(lo, hi T) {
	if lo > hi {
		panic(fmt.Sprintf(
			"via: Clamp called with inverted bounds (lo %v > hi %v)", lo, hi))
	}
	_ = o.update(func(cur T) (T, error) {
		if cur < lo {
			return lo, nil
		}
		if cur > hi {
			return hi, nil
		}
		return cur, nil
	})
}

// SignalNum is the numeric-specialized Signal — same client-mirrored
// reactive value as Signal[T], with a typed Op(ctx) chain.
type SignalNum[T Number] struct{ Signal[T] }

// Op returns a numeric chain bound to ctx.
func (s *SignalNum[T]) Op(ctx *Ctx) *NumOps[T] {
	mustOpCtx(ctx)
	return &NumOps[T]{ops: ops[T]{update: func(fn func(T) (T, error)) error { return s.Update(ctx, fn) }}}
}

// StateTabNum is the numeric-specialized StateTab.
type StateTabNum[T Number] struct{ StateTab[T] }

// Op returns a numeric chain bound to ctx.
func (s *StateTabNum[T]) Op(ctx *Ctx) *NumOps[T] {
	mustOpCtx(ctx)
	return &NumOps[T]{ops: ops[T]{update: func(fn func(T) (T, error)) error { return s.Update(ctx, fn) }}}
}

// StateSessNum is the numeric-specialized StateSess.
type StateSessNum[T Number] struct{ StateSess[T] }

// Op returns a numeric chain bound to ctx.
func (s *StateSessNum[T]) Op(ctx *Ctx) *NumOps[T] {
	mustOpCtx(ctx)
	return &NumOps[T]{ops: ops[T]{update: func(fn func(T) (T, error)) error { return s.Update(ctx, fn) }}}
}

// StateAppNum is the numeric-specialized StateApp.
type StateAppNum[T Number] struct{ StateApp[T] }

// Op returns a numeric chain bound to ctx.
func (a *StateAppNum[T]) Op(ctx *Ctx) *NumOps[T] {
	mustOpCtx(ctx)
	return &NumOps[T]{ops: ops[T]{update: func(fn func(T) (T, error)) error { return a.Update(ctx, fn) }}}
}
