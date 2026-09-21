package site

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"strings"

	"go-via.dev/site/static"
)

var staticETags = staticDigests()

func staticDigests() map[string]string {
	digests := map[string]string{}
	err := fs.WalkDir(static.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := static.FS.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		digests["/static/"+p] = `"` + hex.EncodeToString(sum[:]) + `"`
		return nil
	})
	if err != nil {
		panic(err)
	}
	return digests
}

func staticHandler() http.Handler {
	files := http.StripPrefix("/static/", http.FileServerFS(static.FS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		etag, ok := staticETags[r.URL.Path]
		if !ok {
			// Every file is in the map, so a miss is a directory or an alias
			// spelling of a file; FileServerFS would list the one and serve
			// the other with no ETag or Cache-Control.
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", etag)
		// No immutable tier: nothing here is content-addressed, so every asset
		// is one revalidation away from a redeploy being visible.
		w.Header().Set("Cache-Control", "public, max-age=3600, must-revalidate")
		if noneMatch(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		files.ServeHTTP(w, r)
	})
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
