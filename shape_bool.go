package via

// BoolOps is the chain returned by Op(ctx) on every Bool* reactive
// type; its boolean verbs route through the handle's Update path.
type BoolOps struct {
	ops[bool]
}

// Toggle flips the value.
func (o *BoolOps) Toggle() {
	_ = o.update(func(b bool) (bool, error) { return !b, nil })
}

// True sets the value to true.
func (o *BoolOps) True() {
	_ = o.update(func(bool) (bool, error) { return true, nil })
}

// False sets the value to false.
func (o *BoolOps) False() {
	_ = o.update(func(bool) (bool, error) { return false, nil })
}

// SignalBool is the bool-specialized Signal — client-mirrored reactive
// bool with a typed Op(ctx) chain.
type SignalBool struct{ Signal[bool] }

// Op returns a bool chain bound to ctx.
func (s *SignalBool) Op(ctx *Ctx) *BoolOps {
	mustOpCtx(ctx)
	return &BoolOps{ops: ops[bool]{update: func(fn func(bool) (bool, error)) error { return s.Update(ctx, fn) }}}
}

// StateTabBool is the bool-specialized StateTab.
type StateTabBool struct{ StateTab[bool] }

// Op returns a bool chain bound to ctx.
func (s *StateTabBool) Op(ctx *Ctx) *BoolOps {
	mustOpCtx(ctx)
	return &BoolOps{ops: ops[bool]{update: func(fn func(bool) (bool, error)) error { return s.Update(ctx, fn) }}}
}

// StateSessBool is the bool-specialized StateSess.
type StateSessBool struct{ StateSess[bool] }

// Op returns a bool chain bound to ctx.
func (s *StateSessBool) Op(ctx *Ctx) *BoolOps {
	mustOpCtx(ctx)
	return &BoolOps{ops: ops[bool]{update: func(fn func(bool) (bool, error)) error { return s.Update(ctx, fn) }}}
}

// StateAppBool is the bool-specialized StateApp.
type StateAppBool struct{ StateApp[bool] }

// Op returns a bool chain bound to ctx.
func (a *StateAppBool) Op(ctx *Ctx) *BoolOps {
	mustOpCtx(ctx)
	return &BoolOps{ops: ops[bool]{update: func(fn func(bool) (bool, error)) error { return a.Update(ctx, fn) }}}
}
