package via

import "net/http"

// Middleware processes a request before the page handler runs.
// Call next.ServeHTTP(w, r) to continue the chain; omit it to short-circuit.
type Middleware func(w http.ResponseWriter, r *http.Request, next http.Handler)

// Use registers global middleware that runs on page routes.
func (a *App) Use(mw ...Middleware) {
	a.middleware = append(a.middleware, mw...)
}

func runMiddleware(chain []Middleware, w http.ResponseWriter, r *http.Request, final http.Handler) {
	if len(chain) == 0 {
		final.ServeHTTP(w, r)
		return
	}
	var build func(i int) http.Handler
	build = func(i int) http.Handler {
		if i >= len(chain) {
			return final
		}
		next := build(i + 1)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			chain[i](w, r, next)
		})
	}
	build(0).ServeHTTP(w, r)
}
