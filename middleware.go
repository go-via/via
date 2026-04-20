package via

import "net/http"

type Middleware func(w http.ResponseWriter, r *http.Request, next http.Handler)

func applyMiddleware(chain []Middleware, final http.Handler) http.Handler {
	if len(chain) == 0 {
		return final
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
	return build(0)
}
