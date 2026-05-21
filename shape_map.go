package via

// MapOps is the chain returned by Op(ctx) on every Map* reactive type.
// Embeds Ops[map[K]V] for Apply/To plus the map verbs.
type MapOps[K comparable, V any] struct {
	Ops[map[K]V]
}

// Put writes v at k. Allocates the map if nil.
func (o *MapOps[K, V]) Put(k K, v V) {
	o.Apply(func(cur map[K]V) map[K]V {
		if cur == nil {
			cur = make(map[K]V)
		}
		cur[k] = v
		return cur
	})
}

// Delete removes the entry at k. No-op if absent.
func (o *MapOps[K, V]) Delete(k K) {
	o.Apply(func(cur map[K]V) map[K]V {
		delete(cur, k)
		return cur
	})
}

// Empty replaces the value with nil (empty map).
func (o *MapOps[K, V]) Empty() { o.To(nil) }

// SignalMap is the map-specialized Signal.
type SignalMap[K comparable, V any] struct{ Signal[map[K]V] }

// Op returns a map chain bound to ctx.
func (s *SignalMap[K, V]) Op(ctx *Ctx) *MapOps[K, V] {
	return &MapOps[K, V]{Ops: Ops[map[K]V]{apply: func(fn func(map[K]V) map[K]V) { s.Update(ctx, fn) }}}
}

// StateTabMap is the map-specialized StateTab.
type StateTabMap[K comparable, V any] struct{ StateTab[map[K]V] }

// Op returns a map chain bound to ctx.
func (s *StateTabMap[K, V]) Op(ctx *Ctx) *MapOps[K, V] {
	return &MapOps[K, V]{Ops: Ops[map[K]V]{apply: func(fn func(map[K]V) map[K]V) { s.Update(ctx, fn) }}}
}

// StateSessMap is the map-specialized StateSess.
type StateSessMap[K comparable, V any] struct{ StateSess[map[K]V] }

// Op returns a map chain bound to ctx.
func (s *StateSessMap[K, V]) Op(ctx *Ctx) *MapOps[K, V] {
	return &MapOps[K, V]{Ops: Ops[map[K]V]{apply: func(fn func(map[K]V) map[K]V) { s.Update(ctx, fn) }}}
}

// StateAppMap is the map-specialized StateApp.
type StateAppMap[K comparable, V any] struct{ StateApp[map[K]V] }

// Op returns a map chain bound to ctx.
func (a *StateAppMap[K, V]) Op(ctx *Ctx) *MapOps[K, V] {
	return &MapOps[K, V]{Ops: Ops[map[K]V]{apply: func(fn func(map[K]V) map[K]V) { a.Update(ctx, fn) }}}
}
