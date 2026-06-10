package core

// BumpPresence returns a copy of m with m[code] adjusted by delta, clamped so a
// watcher count never goes negative (max(0, m[code]+delta)). The input map is
// never mutated, so it is safe to apply against a shared StateApp value. A
// dispose without a matching connect (reconnect race) is what makes the clamp
// matter: without it the shared count could drift below zero.
func BumpPresence(m map[string]int, code string, delta int) map[string]int {
	out := make(map[string]int, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	if n := out[code] + delta; n > 0 {
		out[code] = n
	} else {
		out[code] = 0
	}
	return out
}
