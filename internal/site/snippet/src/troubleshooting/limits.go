package troubleshooting

import "github.com/go-via/via"

// snippet:start origin
func newRouter() *via.Router {
	return via.NewRouter(
		// Exactly as the browser sends it: scheme, host, port if not default.
		via.WithTrustedOrigin("https://example.com"),
		via.WithTrustedOrigin("https://app.example.com"),
	)
}

// snippet:end

// snippet:start limits
func newUploadRouter() *via.Router {
	return via.NewRouter(
		via.WithMaxUpload(64<<20), // a PostForm submit's whole body
		via.WithMaxBody(4<<20),    // a JSON action body
	)
}

// snippet:end
