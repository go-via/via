package hcore_test

import (
	"testing"

	"github.com/go-via/via/internal/hcore"
	"github.com/stretchr/testify/assert"
)

func TestURLOrigin_readsTheOriginTheBrowserNavigatesTo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, origin string
		ok               bool
	}{
		{"relative path", "/profile", "", true},
		{"relative query", "?tab=2", "", true},
		{"absolute https", "https://app.example/x", "https://app.example", true},
		{"case and default port folded", "HTTPS://APP.Example:443/x", "https://app.example", true},
		{"http default port folded", "http://app.example:80/", "http://app.example", true},
		{"other port kept", "https://app.example:8443/", "https://app.example:8443", true},
		{"userinfo is not the host", "https://app.example@evil.example/", "https://evil.example", true},
		{"backslash ends the authority", `https://evil.example\@app.example/`, "https://evil.example", true},
		{"backslash in host ends it", `https://evil.example\.app.example/`, "https://evil.example", true},
		{"tab inside the scheme", "ht\ttps://evil.example/", "https://evil.example", true},
		{"leading control characters", "\x01 https://evil.example/", "https://evil.example", true},
		{"missing slashes", "https:evil.example", "", false},
		{"one slash", "https:/evil.example", "", false},
		{"protocol-relative", "//evil.example/", "", false},
		{"backslash protocol-relative", `\/evil.example/`, "", false},
		{"script scheme", "javascript:alert(1)", "", false},
		{"empty", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			origin, ok := hcore.URLOrigin(c.in)
			assert.Equal(t, c.ok, ok)
			assert.Equal(t, c.origin, origin)
		})
	}
}
