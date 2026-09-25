package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
)

// Security is the page on composition, routing, auth, the security floor and the hooks.
type Security struct {
	page
	Compose   demos.Compose
	Routes    demos.RouteNotes
	Auth      demos.Auth
	CSP       demos.CSPNotes
	Lifecycle demos.Lifecycle
}

func NewSecurity(env Env) Security { return Security{page: newPage("/security", env)} }

func (p *Security) PageMeta() via.Meta {
	return p.meta(
		"Composition with Child, mount patterns and ctx.Param, auth as an OnInit check, the mount's Content-Security-Policy, and the OnInit/OnReload/OnConnect hooks.")
}

func (p *Security) View() h.H {
	d := p.doc()
	return d.Page(
		h.P(h.Str("A page is a struct. Its fields are its children, its methods are its actions and its hooks, "+
			"and the document it produces is described by one PageMeta method on the mounted root.")),

		demo.Card(d.H3("Composition"),
			h.P(h.Str("via.Child(p.Left) renders a field into its own container. The argument must be a field "+
				"selector, never a composite literal: the child is copied once per render and a literal would "+
				"re-seed it every time. Each child is keyed by its position among its parent's Child calls, so "+
				"the two counters below have their own container id (look for via-i in the DOM), their own State "+
				"and their own action address.")),
			via.Child(p.Compose), "compose.go"),

		demo.Card(d.H3("Routing"),
			h.P(h.Str("A mount is a path pattern and ctx.Param reads its named segments. An action posts to that "+
				"pattern with its segments filled in, which is why a path param survives the click and a query "+
				"param does not.")),
			via.Child(p.Routes), "routing.go"),

		demo.Card(d.H3("Auth is an OnInit check"),
			h.P(h.Str("There is no guard, no middleware and no protected-route table: OnInit reads "+
				"ctx.Session().Get[User](), the View branches on what it found, and a unit that should not be "+
				"reachable renders a login instead of itself. Signing in is a native form submit, so the handler "+
				"can Put the user, Rotate the session cookie and Redirect — the browser only sends the new cookie on "+
				"the request after this one. Signing out is an ordinary action that Deletes the value and Rotates "+
				"the id again — sign-out is an auth-state change too — and OnReload re-reads the session the "+
				"handler cleared.")),
			via.Child(p.Auth), "auth.go"),

		demo.Card(d.H3("The policy this page carries"),
			h.P(h.Str("Every mount serves a Content-Security-Policy derived from what it declared: the router's "+
				"WithHead assets plus that mount's PageMeta().Assets, built once at Mount and never per request. "+
				"Which is why Assets must be a constant of the type — via reads it off the mounted literal and "+
				"again off a copy with its zero fields filled in, standing in for what OnInit would load, and "+
				"panics when the two disagree: at Mount for a page the probe can see through, or on first render "+
				"for a page it cannot. Everything else in via.Meta is inert escaped text and may vary with the "+
				"request.")),
			via.Child(p.CSP), "csp.go"),

		demo.Card(d.H3("Hooks"),
			h.Div(
				h.P(h.Str("Click the action below to see OnReload, then follow the reload link: the log starts "+
					"again at OnInit, because the List holding it is per connection.")),
				table([]string{"Hook", "Runs when", "Use"},
					[]h.H{h.Code(h.Str("OnInit(*via.Ctx) error")),
						h.Str("Every request that renders the unit: the GET, the stream connect, and each action on a page that is not streaming."),
						h.Str("Load request and session data; register timers and subscriptions.")},
					[]h.H{h.Code(h.Str("OnReload(*via.Ctx) error")),
						h.Str("After one of the unit's actions, before the render that answers it. Skipped behind a Redirect."),
						h.Str("Re-read what the handler mutated. Tick and Listen are no-ops here, so liveness stays the verdict of the GET.")},
					[]h.H{h.Code(h.Str("ctx.OnConnect(fn)")),
						h.Str("Once, when the unit's stream opens, after every Listen has subscribed."),
						h.Str("Acquire something per connection. On a unit nothing made live it never runs.")},
					[]h.H{h.Code(h.Str("ctx.OnDispose(fn)")),
						h.Str("When the unit's connection closes."),
						h.Str("Stop subscriptions, release producers.")},
				),
				h.P(h.Str("On a streaming page the unit outlives the request, so a click runs the handler and "+
					"then OnReload, never OnInit again. OnInit registers timers and subscriptions and reads the "+
					"session, none of which is safe to repeat after a handler has committed a mutation.")),
			),
			via.Child(p.Lifecycle), "lifecycle.go"),
	)
}
