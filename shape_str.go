package via

// StrOps is the chain returned by Op(ctx) on every Str* reactive type.
type StrOps struct {
	Ops[string]
}

// Append concatenates s onto the end.
func (o *StrOps) Append(s string) { o.Apply(func(cur string) string { return cur + s }) }

// Prepend concatenates s onto the start.
func (o *StrOps) Prepend(s string) { o.Apply(func(cur string) string { return s + cur }) }

// Clear replaces the value with the empty string.
func (o *StrOps) Clear() { o.To("") }

// SignalStr is the string-specialized Signal.
type SignalStr struct{ Signal[string] }

// Op returns a string chain bound to ctx.
func (s *SignalStr) Op(ctx *Ctx) *StrOps {
	return &StrOps{Ops: Ops[string]{apply: func(fn func(string) string) { s.Update(ctx, fn) }}}
}

// StateTabStr is the string-specialized StateTab.
type StateTabStr struct{ StateTab[string] }

// Op returns a string chain bound to ctx.
func (s *StateTabStr) Op(ctx *Ctx) *StrOps {
	return &StrOps{Ops: Ops[string]{apply: func(fn func(string) string) { s.Update(ctx, fn) }}}
}

// StateSessStr is the string-specialized StateSess.
type StateSessStr struct{ StateSess[string] }

// Op returns a string chain bound to ctx.
func (s *StateSessStr) Op(ctx *Ctx) *StrOps {
	return &StrOps{Ops: Ops[string]{apply: func(fn func(string) string) { s.Update(ctx, fn) }}}
}

// StateAppStr is the string-specialized StateApp.
type StateAppStr struct{ StateApp[string] }

// Op returns a string chain bound to ctx.
func (a *StateAppStr) Op(ctx *Ctx) *StrOps {
	return &StrOps{Ops: Ops[string]{apply: func(fn func(string) string) { a.Update(ctx, fn) }}}
}
