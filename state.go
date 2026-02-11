package via

type StateHandle[T any] struct {
	id      string
	initial T
}

func State[T any](initial T) *StateHandle[T] {
	return &StateHandle[T]{
		id:      genRandID(),
		initial: initial,
	}
}

func (s *StateHandle[T]) Get(sc *Session) T {
	if sc == nil || sc.s == nil {
		return s.initial
	}
	if val, ok := sc.s.state[s.id]; ok {
		return val.(T)
	}
	return s.initial
}

func (s *StateHandle[T]) Set(sc *Session, value T) {
	if sc == nil || sc.s == nil {
		return
	}
	if sc.mode == sessionModeView {
		sc.warn("State.Set() called during view render; mutation ignored")
		return
	}
	sc.s.state[s.id] = value
	sc.Sync() // Auto-sync on state change
}
