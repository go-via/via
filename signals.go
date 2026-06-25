package via

import (
	"bytes"
	"encoding/json"
)

// writeSignalsAttr writes the page-level Datastar signal declaration as a
// single-quoted HTML attribute: data-signals='{...}'. The signals map is
// marshaled to JSON, then escaped for the single-quoted attribute context
// before being written.
//
// json.Marshal already unicode-escapes <, > and & inside string VALUES (to
// < etc.), so the only HTML-significant character it leaves raw in its
// output is the single quote. Left verbatim, a string signal carrying an
// apostrophe would close this single-quoted attribute early and let an attacker
// graft live attributes (e.g. a data-on-* Datastar expression) onto
// <div id="root">. We therefore entity-encode the apostrophe for the
// single-quoted context. Double quotes are left intact: they are legal inside a
// single-quoted attribute, keep the JSON readable, and the browser hands the
// decoded value to Datastar either way.
func writeSignalsAttr(buf *bytes.Buffer, order []string, initial map[string]any) {
	sig := make(map[string]any, len(order))
	for _, slot := range order {
		sig[slot] = initial[slot]
	}
	raw, _ := json.Marshal(sig)

	buf.WriteString(` data-signals='`)
	for _, b := range raw {
		if b == '\'' {
			buf.WriteString("&#39;")
			continue
		}
		buf.WriteByte(b)
	}
	buf.WriteByte('\'')
}
