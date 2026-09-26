// Package security holds the compile-checked code the /security page shows.
package security

import (
	"os"
	"strconv"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// snippet:start router
func newRouter() *via.Router {
	return via.NewRouter(
		via.WithTrustedOrigin("https://example.com"),
		via.WithSecureCookies(), // the proxy sends no X-Forwarded-Proto
		via.WithSessionKey([]byte(os.Getenv("SESSION_KEY"))),
		via.WithSessionTTL(8*time.Hour),
	)
}

// snippet:end

type Checkout struct{ order int }

func (c *Checkout) View() h.H { return h.Div() }

// snippet:start redirect
func (c *Checkout) Back(ctx *via.Ctx) {
	// A next field of https://evil.example is dropped and logged.
	ctx.Redirect(ctx.Request().FormValue("next"))
}

func (c *Checkout) Pay(ctx *via.Ctx) {
	ctx.RedirectExternal("https://pay.example/checkout/" + strconv.Itoa(c.order))
}

// snippet:end
