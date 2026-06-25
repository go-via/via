package via

import (
	"bytes"
	"strings"
	"testing"
)

// A string signal value can carry attacker-influenced data. The data-signals
// declaration is written inside a single-quoted HTML attribute, so if a raw
// apostrophe survives into the attribute value it closes the attribute early
// and lets the attacker graft new attributes (e.g. a live data-on-load
// Datastar expression) onto <div id="root">, executing script. The serializer
// must neutralise the apostrophe (and ampersand) for the single-quoted context.
func TestDataSignalsAttributeCannotBeBrokenOutOfBySingleQuote(t *testing.T) {
	var buf bytes.Buffer
	payload := `' data-on-load='alert(document.cookie)`
	writeSignalsAttr(&buf, []string{"s0"}, map[string]any{"s0": payload})
	got := buf.String()

	// Isolate the attribute VALUE (between the opening =' and the closing ').
	const open = `data-signals='`
	i := strings.Index(got, open)
	if i < 0 || !strings.HasSuffix(got, "'") {
		t.Fatalf("attribute not single-quote-delimited as expected:\n%s", got)
	}
	value := got[i+len(open) : len(got)-1]

	if strings.Contains(value, "'") {
		t.Fatalf("raw single quote survived inside the attribute value, breakout possible:\n%s", got)
	}
	// The payload's apostrophe must appear only as its entity.
	if !strings.Contains(value, "&#39;") {
		t.Fatalf("apostrophe was not entity-encoded:\n%s", got)
	}
}

// The serializer must still emit a valid, attribute-quoted data-signals
// declaration for ordinary numeric signals — the common case must not regress.
func TestDataSignalsAttributeEmitsNumericDeclaration(t *testing.T) {
	var buf bytes.Buffer
	writeSignalsAttr(&buf, []string{"s0"}, map[string]any{"s0": 0})
	got := buf.String()

	if !strings.Contains(got, `data-signals='{"s0":0}'`) {
		t.Fatalf("numeric signals declaration malformed:\n%s", got)
	}
}
