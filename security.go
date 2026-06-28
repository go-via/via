package via

import (
	"net/http"
	"net/url"
	"strings"
)

// maxActionBody caps the action request body to defend against memory
// exhaustion; 1 MiB is far above any legitimate signal payload.
const maxActionBody = 1 << 20

// maxUploadBytes caps a multipart upload body (OnUpload). Larger than the action
// cap since files are the payload, but still bounded so an upload can't exhaust
// memory/disk.
const maxUploadBytes = 8 << 20

// originAllowed reports whether req may invoke a state-changing action. The
// floor, in order: a WithInsecureOrigin bypass; a WithTrustedOrigin allowlist
// (which wins over the browser's site label, so cross-origin embedding works);
// the browser's Sec-Fetch-Site (only same-origin/none pass); then, absent that,
// an Origin whose host matches the request Host. A request that proves nothing
// about its source fails closed.
func originAllowed(req *http.Request, cfg *config) bool {
	if cfg.insecureOrigin {
		return true
	}
	origin := req.Header.Get("Origin")
	if origin != "" && cfg.trustedOrigins[origin] {
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
	// If the request arrived over TLS, the document origin is https; an http
	// Origin is a scheme downgrade. When req.TLS is nil the real scheme is
	// unknown (a TLS-terminating proxy is common), so scheme is not enforced.
	if req.TLS != nil && u.Scheme != "https" {
		return false
	}
	return sameOriginHost(u, req.Host)
}

// sameOriginHost reports whether the Origin URL's authority matches the request
// Host for same-origin purposes: host comparison is case-insensitive and the
// origin's scheme default port (80 for http, 443 for https) is equivalent to an
// omitted port. The request authority carries no scheme, so either default-port
// form is accepted on its side.
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
