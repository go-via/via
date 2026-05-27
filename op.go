package via

// ops is the internal base embedded by every shape ops type (NumOps,
// BoolOps, StrOps, SliceOps, MapOps). It carries the closure that
// bridges shape verbs to the handle's Update path. Unexported because
// it has no methods of its own; users only ever touch the embedding
// specialized types.
type ops[T any] struct {
	update func(func(T) (T, error)) error
}

// mustOpCtx panics if ctx is nil so the failure points at the user's
// Op(nil) call site rather than firing later inside a verb closure
// with a stack rooted at Signal.Update.
func mustOpCtx(ctx *Ctx) {
	if ctx == nil {
		panic("via: Op called with nil *Ctx")
	}
}
