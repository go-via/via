package echarts

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// The vendored ECharts dist build. Refresh with `go generate` (see
// refresh_assets.sh); bump pinnedVersion in plugin.go in the same change.
//
//go:embed assets/echarts.min.js
var echartsJS []byte

const assetPathPrefix = "/via/assets/echarts/"

// asset is one embedded file, precompressed and content-hashed at
// registration so request handling never gzips or hashes on the fly.
type asset struct {
	name        string
	contentType string
	body        []byte
	gz          []byte
	hash        string
}

func newAsset(name, contentType string, body []byte) *asset {
	sum := sha256.Sum256(body)
	return &asset{
		name:        name,
		contentType: contentType,
		body:        body,
		gz:          gzipBytes(body),
		hash:        hex.EncodeToString(sum[:8]),
	}
}

// path returns the content-addressed URL. The hash segment changes
// whenever the body does, which is what makes the immutable cache
// header safe.
func (a *asset) path() string { return assetPathPrefix + a.hash + "/" + a.name }

func (p *plugin) serveAssets(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, assetPathPrefix)
	hash, name, ok := strings.Cut(rest, "/")
	// A stale hash means the embedded content changed under a cached
	// page; serving the new body at the old URL would poison caches.
	if !ok || name != p.js.name || hash != p.js.hash {
		http.NotFound(w, r)
		return
	}
	writeImmutableAsset(w, r, p.js)
}

func writeImmutableAsset(w http.ResponseWriter, r *http.Request, a *asset) {
	w.Header().Set("Vary", "Accept-Encoding")
	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(a.gz)
		return
	}
	_, _ = w.Write(a.body)
}

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

// sriDigestLengths maps each SRI hash algorithm to its raw digest size,
// the only sizes a well-formed integrity value can decode to.
var sriDigestLengths = map[string]int{"sha256": 32, "sha384": 48, "sha512": 64}

// mustValidIntegrity enforces the SRI grammar (sha256|sha384|sha512,
// a dash, base64 of the digest) so a CDN tag can never ship without a
// verifiable hash. origin prefixes the panic message.
func mustValidIntegrity(origin, integrity string) {
	alg, b64, ok := strings.Cut(integrity, "-")
	want, known := sriDigestLengths[alg]
	if !ok || !known {
		panic(fmt.Sprintf("%s: integrity must be sha256-/sha384-/sha512- followed by base64, got %q",
			origin, integrity))
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != want {
		panic(fmt.Sprintf("%s: integrity digest is not valid base64 of a %s digest: %q",
			origin, alg, integrity))
	}
}

// mustCrossOriginURL rejects anything that isn't an absolute https URL —
// http would let an on-path attacker swap the body before SRI existed in
// the page, and same-origin paths belong to the source options.
func mustCrossOriginURL(origin, url string) {
	if !strings.HasPrefix(url, "https://") {
		panic(fmt.Sprintf("%s: url must be an absolute https:// URL, got %q", origin, url))
	}
}

// mustSameOriginURL rejects cross-origin script/style delivery without
// SRI; the CDN options exist exactly for that case.
func mustSameOriginURL(origin, url string) {
	if url == "" {
		panic(origin + ": url cannot be empty")
	}
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") ||
		strings.HasPrefix(url, "//") {
		panic(origin + ": cross-origin URLs need Subresource Integrity — use WithCDN(url, integrity)")
	}
}
