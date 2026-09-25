package shell

import "strings"

// Version is one entry of the version picker. Base is "" for a build served
// at the root, "/vX.Y" for one served under a prefix on this host, or an
// absolute https URL for docs hosted elsewhere.
type Version struct {
	Label string
	Base  string
}

// Href is path on this version's docs. An external version has no page map,
// so every path lands on its front page.
func (v Version) Href(path string) string {
	if v.external() {
		return v.Base
	}
	return join(v.Base, path)
}

func (v Version) external() bool {
	return strings.HasPrefix(v.Base, "http://") || strings.HasPrefix(v.Base, "https://")
}

// Site is where this build is served: its path prefix, its origin, and the
// versions the picker offers (the first is the latest).
type Site struct {
	Base     string
	Origin   string
	Versions []Version
}

// Href prefixes an absolute site path with the base.
func (s *Site) Href(path string) string { return join(s.Base, path) }

// Current is the version entry this build serves.
func (s *Site) Current() (Version, bool) {
	for _, v := range s.Versions {
		if v.Base == s.Base {
			return v, true
		}
	}
	return Version{}, false
}

// Latest is the first version, or the root when none is configured.
func (s *Site) Latest() Version {
	if len(s.Versions) == 0 {
		return Version{}
	}
	return s.Versions[0]
}

func join(base, path string) string {
	if base == "" {
		return path
	}
	if path == "/" {
		return base + "/"
	}
	return base + path
}
