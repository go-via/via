package via

import (
	"net/http"
	"net/url"
	"strings"
)

// maxActionBody bounds an action body against memory exhaustion; 1 MiB is far
// above any legitimate signal payload. The default for WithMaxBody.
const maxActionBody = 1 << 20

// maxUploadBytes caps a PostForm's multipart body — every native form, not just
// ones with a file input, since the parser can't tell in advance. Only
// maxActionBody of it is kept in RAM regardless. The default for WithMaxUpload.
const maxUploadBytes = 8 << 20

// originAllowed is the "origin floor": the check that a state-changing request
// comes from a host the app trusts, read off Origin/Sec-Fetch-Site. Without
// WithTrustedOrigin it admits every origin here, because a live action and the
// SSE connect carry the per-tab id, which is the CSRF token; a plain action has
// no tab id and is held to plainOriginAllowed instead (see dispatch).
// WithTrustedOrigin turns enforcement on, in this order: the allowlist (which
// wins over the browser's site label, so cross-origin embedding works), then
// Sec-Fetch-Site, then an Origin whose host matches the request Host. Under
// enforcement a request that proves nothing about its source fails closed.
func originAllowed(req *http.Request, cfg *config) bool {
	if len(cfg.trustedOrigins) == 0 {
		return true
	}
	origin := req.Header.Get("Origin")
	if origin != "" && cfg.trustedOrigins[origin] {
		return true
	}
	if site := req.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	return sameOriginSource(req, origin)
}

// plainOriginAllowed is the floor for a plain action when no trusted origin is
// configured: nothing else in that request is a CSRF token, so only a provably
// same-origin request is admitted. Referer is the fallback for a browser that
// sends neither fetch metadata nor Origin; a request carrying none of the
// three fails closed.
func plainOriginAllowed(req *http.Request) bool {
	if site := req.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	src := req.Header.Get("Origin")
	if src == "" {
		src = req.Header.Get("Referer")
	}
	return sameOriginSource(req, src)
}

func sameOriginSource(req *http.Request, src string) bool {
	if src == "" {
		return false
	}
	u, err := url.Parse(src)
	if err != nil {
		return false
	}
	// Over TLS, an http Origin is a scheme downgrade. When req.TLS is nil the
	// real scheme is unknown (a TLS-terminating proxy is common), so scheme is
	// not enforced.
	if req.TLS != nil && u.Scheme != "https" {
		return false
	}
	return sameOriginHost(u, req.Host)
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
