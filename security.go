package via

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-via/via/internal/hcore"
)

// maxActionBody bounds an action body against memory exhaustion; 1 MiB is far
// above any legitimate signal payload. The default for WithMaxBody.
const maxActionBody = 1 << 20

// maxUploadBytes caps a PostForm's multipart body — every native form, not just
// ones with a file input, since the parser can't tell in advance. Only
// maxActionBody of it is kept in RAM regardless. The default for WithMaxUpload.
const maxUploadBytes = 8 << 20

// originAllowed is the "origin floor": the check that a state-changing request
// comes from a host the app trusts, read off Origin/Sec-Fetch-Site. Every
// action and the SSE connect pass through it. By default every origin is
// admitted (the per-tab id is the CSRF token), since a strict default refuses
// every localhost dev setup on a second port or behind a dev proxy.
// WithTrustedOrigin turns enforcement on, in this order: the allowlist (which
// wins over the browser's site label, so cross-origin embedding works), then
// Sec-Fetch-Site, then an Origin whose host matches the request Host. A
// sibling subdomain fails it too, which is the point: SameSite=Lax counts one
// as the same site and sends it the cookie. Under enforcement a request that
// proves nothing about its source fails closed.
func originAllowed(req *http.Request, cfg *config) bool {
	if len(cfg.trustedOrigins) == 0 {
		return true
	}
	origin := req.Header.Get("Origin")
	if norm, ok := hcore.URLOrigin(origin); ok && norm != "" && cfg.trustedOrigins[norm] {
		return true
	}
	if site := req.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// Over https, an http Origin is a scheme downgrade. A forged
	// X-Forwarded-Proto can only make this stricter. With no TLS and no proxy
	// header the real scheme is unknown, so scheme is not enforced.
	if servedOverHTTPS(req) && u.Scheme != "https" {
		return false
	}
	return sameOriginHost(u, req.Host)
}

// routerPolicy is the router-wide configuration a Ctx reads outside the
// session: Redirect's trusted origins and the logger.
type routerPolicy struct {
	trustedOrigins map[string]bool
	log            *slog.Logger
}

// redirectTo is a queued Redirect. Its verdict is taken when it is queued,
// where the request is at hand, and answered by whichever transport ends the
// request: a refused target is logged and dropped.
type redirectTo struct {
	url     string
	refused string
}

func (r redirectTo) LogValue() slog.Value { return slog.StringValue(r.url) }

// refusal re-runs the scheme gate at the sink, so a redirectTo built without
// Redirect or RedirectExternal still cannot navigate to javascript: or data:.
func (r redirectTo) refusal() string {
	if r.refused == "" && !hcore.SafeURL(r.url) {
		return notHTTP
	}
	return r.refused
}

const notHTTP = "not http(s) or relative"

// offSite is Redirect's host gate: "" for a relative target, one on the
// request's host, or one on a WithTrustedOrigin origin; otherwise the reason
// it is refused.
func (c *Ctx) offSite(target string) string {
	origin, ok := hcore.URLOrigin(target)
	switch {
	case !ok:
		return notHTTP
	case origin == "":
		return ""
	case c.policy != nil && c.policy.trustedOrigins[origin]:
		return ""
	case c.req != nil:
		if u, err := url.Parse(origin); err == nil && sameOriginHost(u, c.req.Host) {
			return ""
		}
	}
	return "another host; RedirectExternal leaves the site"
}

// sameOriginHost compares authorities case-insensitively, treating a scheme's
// default port as equivalent to an omitted one. The request authority carries
// no scheme, so either default-port form is accepted on its side.
func sameOriginHost(u *url.URL, reqHost string) bool {
	oh := strings.ToLower(u.Host)
	switch u.Scheme {
	case "http":
		oh = strings.TrimSuffix(oh, ":80")
	case "https":
		oh = strings.TrimSuffix(oh, ":443")
	}
	rh := strings.ToLower(reqHost)
	rhBare := strings.TrimSuffix(strings.TrimSuffix(rh, ":443"), ":80")
	return oh == rh || oh == rhBare
}
