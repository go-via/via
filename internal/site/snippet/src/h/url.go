package h

import (
	"github.com/go-via/via/h"
)

// snippet:start urls
func Links() h.H {
	a := func(u, s string) h.H {
		return h.A(h.Href(u), h.Str(s))
	}
	return h.Nav(
		a("/docs?q=a&b", "rel"),
		a("https://go.dev", "https"),
		a("javascript:alert(1)", "js"),
		a("//evil.example", "//host"),
		a("mailto:ada@x.org", "mailto"),
		a("data:text/html,x", "data"),
	)
}

// snippet:end

const LinksHTML = `<nav>
  <a href="/docs?q=a&amp;b">rel</a>
  <a href="https://go.dev">https</a>
  <a href="#">js</a>
  <a href="#">//host</a>
  <a href="mailto:ada@x.org">mailto</a>
  <a href="#">data</a>
</nav>`
