package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/shell"
)

// Platform is the page on composition, routing, auth and the security floor.
type Platform struct {
	Compose   demos.Compose
	Auth      demos.Auth
	CSP       demos.CSPNotes
	Lifecycle demos.Lifecycle
}

func (p *Platform) PageMeta() via.Meta {
	return shell.Meta("Platform",
		"Composition with Child, auth as an OnInit check, the mount's Content-Security-Policy, and the OnInit/OnReload/OnConnect hooks.")
}

// OnInit mints the session before the auth demo reads it, so a visitor who
// never signs in still has an id the rate limits can key on.
func (p *Platform) OnInit(ctx *via.Ctx) error {
	shell.EnsureSession(ctx)
	return nil
}

func (p *Platform) View() h.H {
	return shell.Page("Platform", shell.Nav[5],
		h.P(h.Str("A page is a struct. Its fields are its children, its methods are its actions and its hooks, "+
			"and the document it produces is described by one PageMeta method on the mounted root.")),

		demo.Card("Composition",
			h.P(h.Str("via.Child(p.Left) renders a field into its own container. The argument must be a field "+
				"selector, never a composite literal: the child is copied once per render and a literal would "+
				"re-seed it every time. Each child is keyed by its position among its parent's Child calls, so "+
				"the two counters below have their own container id (look for via-i in the DOM), their own State "+
				"and their own action address.")),
			via.Child(p.Compose), "compose.go"),

		demo.Card("Auth is an OnInit check",
			h.P(h.Str("There is no guard, no middleware and no protected-route table: OnInit reads "+
				"ctx.Session().Get[User](), the View branches on what it found, and a unit that should not be "+
				"reachable renders a login instead of itself. Signing in is a native form submit, so the handler "+
				"can Put the user, Rotate the session cookie and Redirect — the browser only sends the new cookie on "+
				"the request after this one. Signing out is an ordinary action, and OnReload re-reads the session "+
				"the handler cleared.")),
			via.Child(p.Auth), "auth.go"),

		demo.Card("The policy this page carries",
			h.P(h.Str("Every mount serves a Content-Security-Policy derived from what it declared: the router's "+
				"WithHead assets plus that mount's PageMeta().Assets, built once at Mount and never per request. "+
				"Which is why Assets must be a constant of the type — via reads it off the mounted literal and "+
				"again off a copy with its zero fields filled in, standing in for what OnInit would load, and "+
				"panics when the two disagree. Everything else in via.Meta is inert escaped text and may vary "+
				"with the request.")),
			via.Child(p.CSP), "csp.go"),

		demo.Card("Hooks",
			h.P(h.Str("OnInit runs on every request that renders the unit — the GET, the stream connect, and "+
				"each action on a page that is not streaming. On a streaming page the unit outlives the request, "+
				"so a click runs the handler and then OnReload, never OnInit again: OnInit mints sessions and "+
				"registers timers, neither of which is safe to repeat after a handler has committed a mutation. "+
				"OnConnect runs once, when the stream opens, after every Listen has subscribed; inside OnReload, "+
				"Tick and Listen are no-ops, so liveness stays the verdict of the GET. Click the action to see "+
				"OnReload, then follow the reload link: the log starts again at OnInit because the List is per "+
				"connection.")),
			via.Child(p.Lifecycle), "lifecycle.go"),
	)
}
