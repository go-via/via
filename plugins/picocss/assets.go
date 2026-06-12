package picocss

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// The vendored Pico CSS release. Refresh with `go generate` (see
// refresh_assets.sh); bump pinnedVersion in pico.go in the same change.
//
//go:embed assets/*.min.css
var embeddedCSS embed.FS

const assetPathPrefix = "/via/assets/picocss/"

// asset is one embedded file, precompressed and content-hashed at
// registration so request handling never gzips or hashes on the fly.
type asset struct {
	name        string
	contentType string
	body        []byte
	gz          []byte
	hash        string
}

func newAsset(name string) *asset {
	body, err := embeddedCSS.ReadFile("assets/" + name)
	if err != nil {
		// The embedded tree is fixed at compile time; a miss means the
		// vendored assets are broken, not a runtime condition.
		panic(fmt.Sprintf("picocss: embedded asset %q missing: %v", name, err))
	}
	sum := sha256.Sum256(body)
	return &asset{
		name:        name,
		contentType: "text/css",
		body:        body,
		gz:          gzipBytes(body),
		hash:        hex.EncodeToString(sum[:8]),
	}
}

// path returns the content-addressed URL. The hash segment changes
// whenever the body does, which is what makes the immutable cache
// header safe.
func (a *asset) path() string { return assetPathPrefix + a.hash + "/" + a.name }

func (p *plugin) serveHashedAsset(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, assetPathPrefix)
	hash, name, ok := strings.Cut(rest, "/")
	a := p.assetsByName[name]
	// A stale hash means the embedded content changed under a cached
	// page; serving the new body at the old URL would poison caches.
	if !ok || a == nil || a.hash != hash {
		http.NotFound(w, r)
		return
	}
	writeImmutableAsset(w, r, a)
}

func writeImmutableAsset(w http.ResponseWriter, r *http.Request, a *asset) {
	w.Header().Set("Vary", "Accept-Encoding")
	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if acceptsGzip(r) {
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

func acceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}
