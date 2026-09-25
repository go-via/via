// Package security holds the compile-checked code the /security page shows.
package security

import (
	"os"
	"time"

	"github.com/go-via/via"
)

// snippet:start router
func newRouter() *via.Router {
	return via.NewRouter(
		via.WithTrustedOrigin("https://example.com"),
		via.WithSecureCookies(), // TLS ends at the proxy, so req.TLS is nil here
		via.WithSessionKey([]byte(os.Getenv("SESSION_KEY"))),
		via.WithSessionTTL(8*time.Hour),
	)
}

// snippet:end
