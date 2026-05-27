package via

// StrOps is the chain returned by Op(ctx) on every Str* reactive type.
type StrOps struct {
	ops[string]
}

// Append concatenates s onto the end.
func (o *StrOps) Append(s string) {
	_ = o.update(func(cur string) (string, error) { return cur + s, nil })
}

// Prepend concatenates s onto the start.
func (o *StrOps) Prepend(s string) {
	_ = o.update(func(cur string) (string, error) { return s + cur, nil })
}

// Clear replaces the value with the empty string.
func (o *StrOps) Clear() {
	_ = o.update(func(string) (string, error) { return "", nil })
}

// SignalStr is the string-specialized Signal.
type SignalStr struct{ Signal[string] }

// Op returns a string chain bound to ctx.
func (s *SignalStr) Op(ctx *Ctx) *StrOps {
	mustOpCtx(ctx)
	return &StrOps{ops: ops[string]{update: func(fn func(string) (string, error)) error { return s.Update(ctx, fn) }}}
}

// StateTabStr is the string-specialized StateTab.
type StateTabStr struct{ StateTab[string] }

// Op returns a string chain bound to ctx.
func (s *StateTabStr) Op(ctx *Ctx) *StrOps {
	mustOpCtx(ctx)
	return &StrOps{ops: ops[string]{update: func(fn func(string) (string, error)) error { return s.Update(ctx, fn) }}}
}

// StateSessStr is the string-specialized StateSess.
type StateSessStr struct{ StateSess[string] }

// Op returns a string chain bound to ctx.
func (s *StateSessStr) Op(ctx *Ctx) *StrOps {
	mustOpCtx(ctx)
	return &StrOps{ops: ops[string]{update: func(fn func(string) (string, error)) error { return s.Update(ctx, fn) }}}
}

// StateAppStr is the string-specialized StateApp.
type StateAppStr struct{ StateApp[string] }

// Op returns a string chain bound to ctx.
func (a *StateAppStr) Op(ctx *Ctx) *StrOps {
	mustOpCtx(ctx)
	return &StrOps{ops: ops[string]{update: func(fn func(string) (string, error)) error { return a.Update(ctx, fn) }}}
}
