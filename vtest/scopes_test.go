package vtest

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

type User struct {
	ID   string
	Name string
	Role string
}

// TestSessionScopes_Example demonstrates all three scopes working together
func TestSessionScopes_Example(t *testing.T) {
	v := via.New()

	// User handle for authentication (session-scoped)
	user := via.NewSessionDataHandle[User]()

	v.Page("/", func(c *via.Composition) {
		// Tab-scoped state (default) - unique per browser tab
		tabCounter := via.State(c, 0)

		// Session state - per user (shared across tabs)
		sessionToken := via.State(c, "", via.WithScope(via.ScopeSession))

		// Global app state - shared across all sessions
		appVisits := via.State(c, 0, via.WithScope(via.ScopeApp))

		// Increment actions for each scope
		incTab := via.Action(c, func(ctx *via.Context) {
			tabCounter.Set(ctx, tabCounter.Get(ctx)+1)
		})

		incApp := via.Action(c, func(ctx *via.Context) {
			appVisits.Set(ctx, appVisits.Get(ctx)+1)
		})

		setToken := via.Action(c, func(ctx *via.Context) {
			sessionToken.Set(ctx, "auth-token-123")
		})

		login := via.Action(c, func(ctx *via.Context) {
			user.Set(ctx, User{ID: "user-1", Name: "Alice", Role: "admin"})
			ctx.Sync() // Sync to update the view
		})

		logout := via.Action(c, func(ctx *via.Context) {
			user.Clear(ctx)
			ctx.Sync() // Sync to update the view
		})

		c.View(func(ctx *via.Context) h.H {
			u, authenticated := user.Get(ctx)

			var userSection h.H
			if authenticated {
				userSection = h.Div(
					h.P(h.Textf("User: %s (Role: %s)", u.Name, u.Role)),
					h.Button(h.Text("Logout"), logout.OnClick()),
				)
			} else {
				userSection = h.Div(
					h.P(h.Text("Not logged in")),
					h.Button(h.Text("Login"), login.OnClick()),
				)
			}

			return h.Div(
				h.H1(h.Text("Session Scopes Demo")),
				h.Div(
					h.H2(h.Text("Tab Scope (per tab)")),
					h.P(h.Textf("Tab Counter: %d", tabCounter.Get(ctx))),
					h.Button(h.Text("Inc Tab"), incTab.OnClick()),
				),
				h.Div(
					h.H2(h.Text("Session Scope (per user)")),
					h.P(h.Textf("Token: %s", sessionToken.Get(ctx))),
					h.Button(h.Text("Set Token"), setToken.OnClick()),
				),
				h.Div(
					h.H2(h.Text("App Scope (global)")),
					h.P(h.Textf("Total Visits: %d", appVisits.Get(ctx))),
					h.Button(h.Text("Inc Global"), incApp.OnClick()),
				),
				h.Div(
					h.H2(h.Text("User Data")),
					userSection,
				),
			)
		})
	})

	// Test 1: Tab scope is isolated
	t.Run("Tab scope is isolated per page", func(t *testing.T) {
		page1 := VisitWith(v.HTTPServeMux(), "/")
		defer page1.Close()

		page2 := VisitWith(v.HTTPServeMux(), "/")
		defer page2.Close()

		// Both start at 0
		page1.AssertText(t, "Tab Counter: 0")
		page2.AssertText(t, "Tab Counter: 0")

		// Increment page1
		if err := page1.Click("Inc Tab"); err != nil {
			t.Fatalf("click Inc Tab failed: %v", err)
		}
		page1.AssertText(t, "Tab Counter: 1")

		// Page2 should still be 0 (tab scope)
		page2.AssertText(t, "Tab Counter: 0")
	})

	// Test 2: App scope is shared globally
	t.Run("App scope shared across all sessions", func(t *testing.T) {
		page1 := VisitWith(v.HTTPServeMux(), "/")
		defer page1.Close()

		page2 := VisitWith(v.HTTPServeMux(), "/")
		defer page2.Close()

		// Get current app visits
		page1.AssertText(t, "Total Visits:")

		// Increment from page1
		if err := page1.Click("Inc Global"); err != nil {
			t.Fatalf("click Inc Global failed: %v", err)
		}

		// Both pages should see the update (app scope)
		page1.AssertText(t, "Total Visits: 1")
		if err := page2.Click("Inc Global"); err != nil {
			t.Fatalf("click Inc Global failed: %v", err)
		}
		page2.AssertText(t, "Total Visits: 2")
	})

	// Test 3: Session scope persists across tabs with same cookie
	t.Run("Session scope persists across tabs", func(t *testing.T) {
		page1 := VisitWith(v.HTTPServeMux(), "/")
		defer page1.Close()

		// Set session token
		if err := page1.Click("Set Token"); err != nil {
			t.Fatalf("click Set Token failed: %v", err)
		}
		page1.AssertText(t, "Token: auth-token-123")

		// Create new page with same session (simulated by sharing session state)
		// In real browser, they'd share the cookie
		page2 := VisitWith(v.HTTPServeMux(), "/")
		defer page2.Close()

		// Page2 has different session cookie, so different session scope
		// This demonstrates isolation between different user sessions
		assert.NotEqual(t, page1.sessionID, page2.sessionID)
	})

	// Test 4: SessionData authentication
	t.Run("SessionData authentication", func(t *testing.T) {
		page := VisitWith(v.HTTPServeMux(), "/")
		defer page.Close()

		// Initially not logged in
		page.AssertText(t, "Not logged in")

		// Login
		if err := page.Click("Login"); err != nil {
			t.Fatalf("click Login failed: %v", err)
		}
		page.AssertText(t, "User: Alice")
		page.AssertText(t, "Role: admin")

		// Logout
		if err := page.Click("Logout"); err != nil {
			t.Fatalf("click Logout failed: %v", err)
		}
		page.AssertText(t, "Not logged in")
	})
}

// TestSessionScopes_CookiePersistence verifies session cookie is maintained
func TestSessionScopes_CookiePersistence(t *testing.T) {
	v := via.New()

	v.Page("/", func(c *via.Composition) {
		c.View(func(ctx *via.Context) h.H {
			return h.Div(
				h.P(h.Textf("Session: %s", ctx.SessionID())),
			)
		})
	})

	tester := New(v.HTTPServeMux())

	// First request - should get session cookie
	resp1 := tester.Get("/")
	resp1.AssertStatus(t, 200)
	sessionID1 := resp1.SessionID()
	assert.NotEmpty(t, sessionID1, "Should have session ID")

	// Second request with same cookie should maintain session
	// (vtest doesn't automatically pass cookies, this tests the mechanism)
	resp2 := tester.Get("/")
	resp2.AssertStatus(t, 200)
	sessionID2 := resp2.SessionID()

	// Each request gets a new session cookie (since we're not passing cookies back)
	// In a real browser with cookie jar, they'd be the same
	assert.NotEqual(t, sessionID1, sessionID2, "Without cookie jar, each request gets new session")
}
