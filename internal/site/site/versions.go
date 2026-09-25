package site

import (
	"fmt"
	"strings"

	"go-via.dev/site/shell"
)

// ParseVersions reads VIA_VERSIONS: comma-separated label=base pairs, latest
// first, where base is "/" for the root, "/vX.Y" for a prefix on this host,
// or an http(s) URL for docs hosted elsewhere. Empty is no picker.
func ParseVersions(raw string) ([]shell.Version, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []shell.Version
	labels, bases := map[string]bool{}, map[string]bool{}
	for _, pair := range strings.Split(raw, ",") {
		label, base, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("site: VIA_VERSIONS: %q has no =", strings.TrimSpace(pair))
		}
		label, base = strings.TrimSpace(label), strings.TrimSpace(base)
		if label == "" {
			return nil, fmt.Errorf("site: VIA_VERSIONS: %q has an empty label", strings.TrimSpace(pair))
		}
		switch {
		case strings.HasPrefix(base, "http://"), strings.HasPrefix(base, "https://"):
		case strings.HasPrefix(base, "/"):
			base = strings.TrimSuffix(base, "/")
		default:
			return nil, fmt.Errorf("site: VIA_VERSIONS: %s's base %q is neither a /path nor an http(s) URL", label, base)
		}
		if labels[label] {
			return nil, fmt.Errorf("site: VIA_VERSIONS: label %s appears twice", label)
		}
		if bases[base] {
			return nil, fmt.Errorf("site: VIA_VERSIONS: base %q appears twice", base)
		}
		labels[label], bases[base] = true, true
		out = append(out, shell.Version{Label: label, Base: base})
	}
	return out, nil
}
