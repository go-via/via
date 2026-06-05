package core

// codeAlphabet is a Crockford-ish base32 set (no I/L/O/U to dodge confusion).
const codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Code maps a non-negative counter/seed to a short deterministic room code.
// Negative n is treated as its absolute value; 0 yields the first symbol.
func Code(n int64) string {
	if n < 0 {
		n = -n
	}
	if n == 0 {
		return codeAlphabet[:1]
	}
	var b []byte
	for n > 0 {
		b = append(b, codeAlphabet[n%32])
		n /= 32
	}
	// reverse for big-endian, stable ordering
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}
