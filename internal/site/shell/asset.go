package shell

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"strings"

	"go-via.dev/site/static"
)

var digests = digestStatic()

func digestStatic() map[string]string {
	out := digestMap{}
	if err := fs.WalkDir(static.FS, ".", out.add); err != nil {
		panic(err)
	}
	return out
}

type digestMap map[string]string

func (m digestMap) add(p string, d fs.DirEntry, err error) error {
	if err != nil || d.IsDir() {
		return err
	}
	b, err := static.FS.ReadFile(p)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	m["/static/"+p] = hex.EncodeToString(sum[:])
	return nil
}

// Digest is the hex sha256 of the embedded asset at path ("/static/…").
func Digest(path string) (string, bool) {
	d, ok := digests[path]
	return d, ok
}

// Asset is path ("/static/x") at its fingerprinted URL, "/static/<hash8>/x",
// which the static handler serves as immutable. It panics on a file that is
// not embedded: a link to it would 404 on every page.
func (s *Site) Asset(path string) string {
	d, ok := digests[path]
	if !ok {
		panic("shell: no embedded asset " + path)
	}
	return s.Href("/static/" + d[:8] + "/" + strings.TrimPrefix(path, "/static/"))
}
