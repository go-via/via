package core

import "strings"

// PollChoices parses a comma-separated poll-choices string into a cleaned,
// order-preserving slice: each entry is trimmed and blank entries are dropped.
// It returns nil when no non-blank choice remains, so callers can treat an
// empty result as "no valid ballot" without a separate length dance.
func PollChoices(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
