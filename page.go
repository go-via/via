package via

// Page is an embeddable zero-state struct that supplies no-op defaults
// for the optional lifecycle hooks: OnInit, OnConnect, and OnDispose.
// Embed it to make the hook surface discoverable in your editor — type
// `p.On...` after embedding and the completion list shows what you can
// override.
//
//	type Profile struct {
//	    via.Page
//	    UserID int `path:"id"`
//	    user   *user
//	}
//
//	func (p *Profile) OnInit(ctx *via.Ctx) error {
//	    p.user = loadUser(p.UserID)
//	    return nil
//	}
//
//	func (p *Profile) View(ctx *via.CtxR) h.H { ... }
//
// Embedding is optional. Compositions that don't embed Page work
// exactly as before — Mount detects whichever lifecycle methods are
// defined and ignores the rest. View remains required and has no
// default; Mount panics at registration if it's missing.
//
// The embedded methods are value-receiver so they cost nothing to
// embed (no field, no pointer indirection); the typed-dispatch path
// in newCtx binds them like any other method.
type Page struct{}

// OnInit is the no-op default. Override on the embedding composition
// to do work before the first View renders on the page-load request.
func (Page) OnInit(*Ctx) error { return nil }

// OnConnect is the no-op default. Override to do per-tab setup the
// first time the SSE stream opens (bots that never open SSE don't
// trigger it).
func (Page) OnConnect(*Ctx) error { return nil }

// OnDispose is the no-op default. Override to release per-tab
// resources when the tab is closed, swept by the ctx-TTL, or the app
// shuts down.
func (Page) OnDispose(*Ctx) {}
