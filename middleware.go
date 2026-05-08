package via

import "net/http"

type Middleware func(w http.ResponseWriter, r *http.Request, next http.Handler)

func applyMiddleware(chain []Middleware, final http.Handler) http.Handler {
	// Wrap from the inside out so chain[0] ends up as the outermost
	// middleware and runs first per request — the canonical Go pattern.
	for i := len(chain) - 1; i >= 0; i-- {
		mw, next := chain[i], final
		final = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mw(w, r, next)
		})
	}
	return final
}
