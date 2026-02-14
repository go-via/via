package via

type Scope int

const (
	ScopeTab     Scope = iota // default - per browser tab
	ScopeSession              // per user session (via cookie)
	ScopeApp                  // global app-wide
)

type StateOption interface {
	apply(*stateOpts)
}

type stateOpts struct {
	scope Scope
}

func WithScope(s Scope) StateOption {
	return &scopeOption{scope: s}
}

type scopeOption struct {
	scope Scope
}

func (o *scopeOption) apply(opts *stateOpts) {
	opts.scope = o.scope
}

type StateHandle[T any] struct {
	id      string
	initial T
	scope   Scope
}

func State[T any](c *Composition, initial T, opts ...StateOption) *StateHandle[T] {
	if c.viewCalled {
		panic("State() called after View() - state must be registered before View() is called")
	}

	stateOpts := &stateOpts{scope: ScopeTab}
	for _, opt := range opts {
		opt.apply(stateOpts)
	}

	idStr := genRandID()

	c.states = append(c.states, stateRegistration{
		id:      idStr,
		initial: initial,
		scope:   stateOpts.scope,
	})

	return &StateHandle[T]{
		id:      idStr,
		initial: initial,
		scope:   stateOpts.scope,
	}
}

func (s *StateHandle[T]) Get(sc *Session) T {
	if sc == nil {
		return s.initial
	}

	switch s.scope {
	case ScopeApp:
		if sc.v == nil {
			return s.initial
		}
		sc.v.appStateMu.RLock()
		defer sc.v.appStateMu.RUnlock()
		if val, ok := sc.v.appState[s.id]; ok {
			return val.(T)
		}
		return s.initial
	case ScopeSession:
		if sc.v == nil || sc.sessionID == "" {
			return s.initial
		}
		sc.v.sessionStateMu.RLock()
		defer sc.v.sessionStateMu.RUnlock()
		if sessionData, ok := sc.v.sessionState[sc.sessionID]; ok {
			if val, ok := sessionData[s.id]; ok {
				return val.(T)
			}
		}
		return s.initial
	case ScopeTab:
		fallthrough
	default:
		if sc.s == nil {
			return s.initial
		}
		if val, ok := sc.s.state[s.id]; ok {
			return val.(T)
		}
		return s.initial
	}
}

func (s *StateHandle[T]) TestID() string {
	return s.id
}

func (s *StateHandle[T]) Set(sc *Session, value T) {
	if sc == nil {
		return
	}
	if sc.mode == sessionModeView {
		sc.warn("State.Set() called during view render; mutation ignored")
		return
	}

	switch s.scope {
	case ScopeApp:
		if sc.v == nil {
			return
		}
		sc.v.appStateMu.Lock()
		sc.v.appState[s.id] = value
		sc.v.appStateMu.Unlock()
		sc.Sync()
	case ScopeSession:
		if sc.v == nil || sc.sessionID == "" {
			return
		}
		sc.v.sessionStateMu.Lock()
		if sc.v.sessionState[sc.sessionID] == nil {
			sc.v.sessionState[sc.sessionID] = make(map[string]any)
		}
		sc.v.sessionState[sc.sessionID][s.id] = value
		sc.v.sessionStateMu.Unlock()
		sc.Sync()
	case ScopeTab:
		fallthrough
	default:
		if sc.s == nil {
			return
		}
		sc.s.state[s.id] = value
		sc.Sync()
	}
}
