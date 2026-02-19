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

func (s *StateHandle[T]) Get(ctx *Context) T {
	if ctx == nil {
		return s.initial
	}

	switch s.scope {
	case ScopeApp:
		if ctx.v == nil {
			return s.initial
		}
		ctx.v.appStateMu.RLock()
		defer ctx.v.appStateMu.RUnlock()
		if val, ok := ctx.v.appState[s.id]; ok {
			return val.(T)
		}
		return s.initial
	case ScopeSession:
		if ctx.v == nil || ctx.sessionID == "" {
			return s.initial
		}
		ctx.v.sessions.stateMu.RLock()
		defer ctx.v.sessions.stateMu.RUnlock()
		if sessionData, ok := ctx.v.sessions.state[ctx.sessionID]; ok {
			if val, ok := sessionData[s.id]; ok {
				return val.(T)
			}
		}
		return s.initial
	case ScopeTab:
		fallthrough
	default:
		if ctx.store == nil {
			return s.initial
		}
		if val, ok := ctx.store.state[s.id]; ok {
			return val.(T)
		}
		return s.initial
	}
}

func (s *StateHandle[T]) TestID() string {
	return s.id
}

func (s *StateHandle[T]) Set(ctx *Context, value T) {
	if ctx == nil {
		return
	}
	if ctx.mode == sessionModeView {
		ctx.warn("State.Set() called during view render; mutation ignored")
		return
	}

	switch s.scope {
	case ScopeApp:
		if ctx.v == nil {
			return
		}
		ctx.v.appStateMu.Lock()
		ctx.v.appState[s.id] = value
		ctx.v.appStateMu.Unlock()
		ctx.Sync()
	case ScopeSession:
		if ctx.v == nil || ctx.sessionID == "" {
			return
		}
		ctx.v.sessions.stateMu.Lock()
		if ctx.v.sessions.state[ctx.sessionID] == nil {
			ctx.v.sessions.state[ctx.sessionID] = make(map[string]any)
		}
		ctx.v.sessions.state[ctx.sessionID][s.id] = value
		ctx.v.sessions.stateMu.Unlock()
		ctx.Sync()
	case ScopeTab:
		fallthrough
	default:
		if ctx.store == nil {
			return
		}
		ctx.store.state[s.id] = value
		ctx.Sync()
	}
}
