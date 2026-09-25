package via

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// randomToken returns a 128-bit (the OWASP recommendation) URL-safe token,
// backing session ids and tab ids alike. rand.Read failing is an unrecoverable
// infrastructure fault, so it panics.
func randomToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("via: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// sha256Source returns the CSP source expression admitting exactly src. CSP
// hash sources are standard base64 (padded), not the URL-safe alphabet used
// elsewhere in via — the browser rejects a mismatch silently.
//
// The hash is of src as the browser holds it after parsing, which is what it
// checks: the input stream preprocessor turns CRLF and lone CR into LF, and the
// script-data and RAWTEXT tokenizer states emit U+FFFD for NUL (HTML spec,
// "Preprocessing the input stream" and the "Script data" and "RAWTEXT" states).
func sha256Source(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	src = strings.ReplaceAll(src, "\x00", "\uFFFD")
	sum := sha256.Sum256([]byte(src))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// cspHeader is the strict Content-Security-Policy an element-patch response
// carries. A patch is a fragment, not a document: it loads no assets of its
// own, so it gets the floor policy and not any mount's widened one.
//
// via admits its own inline scripts by hash, not by nonce. A hash authorises
// exactly the bytes via ships, so publishing it costs nothing and leaves no
// token for an injected <script nonce="…"> to borrow; it is also identical
// across pods and restarts with no shared signing key, which is what an
// action's injected redirect script needs when another pod served the document.
//
// 'unsafe-eval' is required, not optional: the bundled Datastar client compiles
// every data-* expression with the Function constructor, which CSP gates behind
// it. Drop it and every action binding is silently dead in the browser while
// every server-side test passes.
//
// style-src carries no inline allowance by default: via emits no <style>
// element and no style attribute (the reconnect banner's rules go through a
// constructed stylesheet, which CSP does not gate). A declared inline Style
// adds its own hash.
var cspHeader = buildCSP(Assets{}, Assets{})

// buildCSP derives a mount's policy from the router-wide assets plus that
// mount's own, so it is exactly as wide as the app declared and stays a pure
// function of the declaration — every pod serving that config serves
// byte-identical bytes. Called once per mount, never per request.
func buildCSP(global, page Assets) string {
	script := &srcSet{}
	script.add("'self'", "'unsafe-eval'", sha256Source(eventsInit), sha256Source(reconnectInit), sha256Source(redirectInit))
	style := &srcSet{}
	style.add("'self'")
	font, img := &srcSet{}, &srcSet{}
	font.add("'self'")
	img.add("'self'")
	base := [4]int{len(script.src), len(style.src), len(font.src), len(img.src)}

	for _, a := range [2]Assets{global, page} {
		for _, s := range a.Scripts {
			if s.Inline != "" {
				script.add(sha256Source(s.Inline))
			}
			script.addOrigin(s.Src)
		}
		for _, s := range a.Styles {
			if s.Inline != "" {
				style.add(sha256Source(s.Inline))
			}
			style.addOrigin(s.Href)
		}
		for _, p := range a.Preload {
			switch p.As {
			case "script":
				script.addOrigin(p.Href)
			case "style":
				style.addOrigin(p.Href)
			case "font":
				font.addOrigin(p.Href)
			case "image":
				img.addOrigin(p.Href)
			}
		}
		for _, o := range a.FontOrigins {
			font.add(originOf(o))
		}
	}

	csp := "default-src 'self'; script-src " + script.join() + "; style-src " + style.join() + "; "
	if len(font.src) > base[2] {
		csp += "font-src " + font.join() + "; "
	}
	if len(img.src) > base[3] {
		csp += "img-src " + img.join() + "; "
	}
	return csp + "object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'self'"
}

// srcSet accumulates one directive's sources in declaration order, without
// repeats — two pages on the same CDN must not double it in the header.
type srcSet struct {
	src  []string
	seen map[string]bool
}

func (s *srcSet) add(vals ...string) {
	for _, v := range vals {
		if v == "" || s.seen[v] {
			continue
		}
		if s.seen == nil {
			s.seen = map[string]bool{}
		}
		s.seen[v] = true
		s.src = append(s.src, v)
	}
}

// addOrigin admits the origin of an absolute asset URL; a relative one is
// already covered by 'self'.
func (s *srcSet) addOrigin(rawURL string) { s.add(originOf(rawURL)) }

func (s *srcSet) join() string { return strings.Join(s.src, " ") }
