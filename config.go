package via

// config holds Register's optional settings. The zero set is the secure
// default: the action endpoint admits only same-origin requests. config is
// mutated only by Option values during Register; the option set is closed —
// users compose the provided WithX constructors, never author an option.
type config struct {
	trustedOrigins map[string]bool
	insecureOrigin bool
	theme          bool
}

// Option configures a Register call.
type Option func(*config)

func newConfig(opts []Option) *config {
	c := &config{trustedOrigins: map[string]bool{}}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithTrustedOrigin allowlists an exact origin (scheme://host[:port], as the
// browser sends it in the Origin header) for the action endpoint, so a known
// cross-origin embedder is admitted even though the browser labels its requests
// cross-site.
func WithTrustedOrigin(origin string) Option {
	return func(c *config) { c.trustedOrigins[origin] = true }
}

// WithInsecureOrigin disables the action endpoint's origin floor entirely. Use
// it only for non-browser clients or local development — it removes the CSRF
// defense.
func WithInsecureOrigin() Option {
	return func(c *config) { c.insecureOrigin = true }
}

// WithTheme injects a small classless stylesheet into the page <head> so plain
// semantic markup (h1, ul, li, form, input, button) looks intentional — no
// class soup in the View. The stylesheet is nonce'd and admitted by the CSP.
// Omit it for an unstyled page.
func WithTheme() Option {
	return func(c *config) { c.theme = true }
}
