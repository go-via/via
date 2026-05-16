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

func htmlEscape(s string) string {
	i := needsEscape(s)
	if i < 0 {
		return s
	}
	// Worst-case growth: '&' becomes "&amp;" (+4), '<' becomes "&lt;" (+3),
	// pre-grow to s+len*4 to avoid one realloc on dense inputs.
	out := make([]byte, 0, len(s)+8)
	out = append(out, s[:i]...)
	for ; i < len(s); i++ {
		c := s[i]
		switch c {
		case '<':
			out = append(out, "&lt;"...)
		case '>':
			out = append(out, "&gt;"...)
		case '&':
			out = append(out, "&amp;"...)
		case '\'':
			out = append(out, "&#39;"...)
		case '"':
			out = append(out, "&#34;"...)
		case 0:
			// Stdlib (text/template.HTMLEscape) replaces NUL with the
			// Unicode replacement character — match that so output is
			// byte-identical to html/template.HTMLEscapeString.
			out = append(out, "�"...)
		default:
			out = append(out, c)
		}
	}
	return string(out)
}

// htmlEscapeBytes returns the escaped bytes; reuses the s backing array
// when no character needed replacement.
func htmlEscapeBytes(s string) []byte {
	i := needsEscape(s)
	if i < 0 {
		// Caller wants bytes; we still need one alloc to detach from the
		// string heap. Pre-grow to len(s) only.
		out := make([]byte, len(s))
		copy(out, s)
		return out
	}
	out := make([]byte, 0, len(s)+8)
	out = append(out, s[:i]...)
	for ; i < len(s); i++ {
		c := s[i]
		switch c {
		case '<':
			out = append(out, "&lt;"...)
		case '>':
			out = append(out, "&gt;"...)
		case '&':
			out = append(out, "&amp;"...)
		case '\'':
			out = append(out, "&#39;"...)
		case '"':
			out = append(out, "&#34;"...)
		case 0:
			// Stdlib (text/template.HTMLEscape) replaces NUL with the
			// Unicode replacement character — match that so output is
			// byte-identical to html/template.HTMLEscapeString.
			out = append(out, "�"...)
		default:
			out = append(out, c)
		}
	}
	return out
}
