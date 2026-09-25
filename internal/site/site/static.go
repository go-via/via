package site

import (
	"net/http"
	"strings"

	"go-via.dev/site/shell"
	"go-via.dev/site/static"
)

const (
	immutable  = "public, max-age=31536000, immutable"
	revalidate = "public, max-age=3600, must-revalidate"
)

// staticHandler serves /static/x and its fingerprinted spelling
// /static/<hash8>/x. Only a fingerprint that matches the file is cached
// immutably; any other (a page rendered before a redeploy, or a font that
// site.css names relative to its own fingerprinted URL) gets the current file
// on the revalidating tier.
func staticHandler() http.Handler {
	files := http.StripPrefix("/static/", http.FileServerFS(static.FS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		path, cache := r.URL.Path, revalidate
		if fp, rest, ok := fingerprint(path); ok {
			path = "/static/" + rest
			if d, ok := shell.Digest(path); ok && d[:8] == fp {
				cache = immutable
			}
		}
		digest, ok := shell.Digest(path)
		if !ok {
			// Every file is in the map, so a miss is a directory or an alias
			// spelling of a file; FileServerFS would list the one and serve
			// the other with no ETag or Cache-Control.
			http.NotFound(w, r)
			return
		}
		etag := `"` + digest + `"`
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", cache)
		if noneMatch(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		r2 := *r
		u := *r.URL
		u.Path, u.RawPath = path, ""
		r2.URL = &u
		files.ServeHTTP(w, &r2)
	})
}

// fingerprint splits "/static/<8 hex>/rest".
func fingerprint(path string) (fp, rest string, ok bool) {
	tail, ok := strings.CutPrefix(path, "/static/")
	if !ok {
		return "", "", false
	}
	fp, rest, ok = strings.Cut(tail, "/")
	if !ok || len(fp) != 8 || strings.Trim(fp, "0123456789abcdef") != "" {
		return "", "", false
	}
	return fp, rest, true
}

// noneMatch is RFC 9110's weak comparison: "*" matches anything served, and a
// W/ prefix is ignored, since a proxy may weaken a tag on its way back.
func noneMatch(header, etag string) bool {
	for _, tag := range strings.Split(header, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == etag {
			return true
		}
	}
	return false
}
