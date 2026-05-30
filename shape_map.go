package via

// MapOps is the chain returned by Op(ctx) on every Map* reactive type;
// its map verbs route through the handle's Update path.
type MapOps[K comparable, V any] struct {
	ops[map[K]V]
}

// Put writes v at k. Allocates the map if nil.
func (o *MapOps[K, V]) Put(k K, v V) {
	_ = o.update(func(cur map[K]V) (map[K]V, error) {
		if cur == nil {
			cur = make(map[K]V)
		}
		cur[k] = v
		return cur, nil
	})
}

// Delete removes the entry at k. No-op if absent.
func (o *MapOps[K, V]) Delete(k K) {
	_ = o.update(func(cur map[K]V) (map[K]V, error) {
		delete(cur, k)
		return cur, nil
	})
}

// Empty replaces the value with nil (empty map).
func (o *MapOps[K, V]) Empty() {
	_ = o.update(func(map[K]V) (map[K]V, error) { return nil, nil })
}

// SignalMap is the map-specialized Signal.
type SignalMap[K comparable, V any] struct{ Signal[map[K]V] }

// Op returns a map chain bound to ctx.
func (s *SignalMap[K, V]) Op(ctx *Ctx) *MapOps[K, V] {
	mustOpCtx(ctx)
	return &MapOps[K, V]{ops: ops[map[K]V]{update: func(fn func(map[K]V) (map[K]V, error)) error { return s.Update(ctx, fn) }}}
}

// StateTabMap is the map-specialized StateTab.
type StateTabMap[K comparable, V any] struct{ StateTab[map[K]V] }

// Op returns a map chain bound to ctx.
func (s *StateTabMap[K, V]) Op(ctx *Ctx) *MapOps[K, V] {
	mustOpCtx(ctx)
	return &MapOps[K, V]{ops: ops[map[K]V]{update: func(fn func(map[K]V) (map[K]V, error)) error { return s.Update(ctx, fn) }}}
}

// StateSessMap is the map-specialized StateSess.
type StateSessMap[K comparable, V any] struct{ StateSess[map[K]V] }

// Op returns a map chain bound to ctx.
func (s *StateSessMap[K, V]) Op(ctx *Ctx) *MapOps[K, V] {
	mustOpCtx(ctx)
	return &MapOps[K, V]{ops: ops[map[K]V]{update: func(fn func(map[K]V) (map[K]V, error)) error { return s.Update(ctx, fn) }}}
}

// StateAppMap is the map-specialized StateApp.
type StateAppMap[K comparable, V any] struct{ StateApp[map[K]V] }

// Op returns a map chain bound to ctx.
func (a *StateAppMap[K, V]) Op(ctx *Ctx) *MapOps[K, V] {
	mustOpCtx(ctx)
	return &MapOps[K, V]{ops: ops[map[K]V]{update: func(fn func(map[K]V) (map[K]V, error)) error { return a.Update(ctx, fn) }}}
}
