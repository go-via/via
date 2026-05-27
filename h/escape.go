package h

// HTML-escape semantics match html/template.HTMLEscapeString: replace
// the six characters '<', '>', '&', '\'', '"', and (per Go stdlib) the
// optional NUL.
//
// The escape is hot — it runs on every Text/Attr construction. We
// hand-roll a single-pass scanner that returns the original string
// unchanged when nothing needs replacement (zero allocation, common
// case) and builds the output once otherwise.

func needsEscape(s string) int {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '<' || c == '>' || c == '&' || c == '\'' || c == '"' || c == 0 {
			return i
		}
	}
	return -1
}

// appendEscaped appends s to dst with the six HTML-escape replacements
// applied. The fast path — no character needed replacement — appends a
// straight copy. NUL is replaced with the Unicode replacement
// character, byte-identical to html/template.HTMLEscapeString.
func appendEscaped(dst []byte, s string) []byte {
	i := needsEscape(s)
	if i < 0 {
		return append(dst, s...)
	}
	dst = append(dst, s[:i]...)
	for ; i < len(s); i++ {
		c := s[i]
		switch c {
		case '<':
			dst = append(dst, "&lt;"...)
		case '>':
			dst = append(dst, "&gt;"...)
		case '&':
			dst = append(dst, "&amp;"...)
		case '\'':
			dst = append(dst, "&#39;"...)
		case '"':
			dst = append(dst, "&#34;"...)
		case 0:
			dst = append(dst, "�"...)
		default:
			dst = append(dst, c)
		}
	}
	return dst
}

func htmlEscape(s string) string {
	if needsEscape(s) < 0 {
		return s
	}
	// Worst-case growth: '&' becomes "&amp;" (+4), '<' becomes "&lt;" (+3),
	// pre-grow to s+8 to avoid one realloc on dense inputs.
	return string(appendEscaped(make([]byte, 0, len(s)+8), s))
}

// htmlEscapeBytes returns the escaped bytes. Always allocates so the
// returned slice does not alias the input string's backing array.
func htmlEscapeBytes(s string) []byte {
	if needsEscape(s) < 0 {
		out := make([]byte, len(s))
		copy(out, s)
		return out
	}
	return appendEscaped(make([]byte, 0, len(s)+8), s)
}
