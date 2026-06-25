package via_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// serve mounts root behind one httptest server, exercising signals through the
// public via.Register surface (no white-box reach into writeSignalsAttr).
func serve[T any](t *testing.T, root T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(via.Register(root))
	t.Cleanup(srv.Close)
	return srv
}

// numComp renders a single client-resident numeric signal, so the page-level
// data-signals declaration is non-empty (the server-state counter declares none).
type numComp struct{ n via.Num }

func (c *numComp) View() h.H { return h.Div(c.n.Node()) }

// The page-level data-signals declaration must carry an ordinary numeric signal
// verbatim — if the common case regressed, the client would hydrate nothing.
func TestDataSignalsDeclaresNumericSignalForHydration(t *testing.T) {
	_, body := do(t, serve(t, numComp{}), http.MethodGet, "/", "")

	if !strings.Contains(body, `data-signals='{"s0":0}'`) {
		t.Fatalf("numeric signal declaration missing/malformed\n---page---\n%s", body)
	}
}

// nameComp is a string signal plus a no-op action. The action lets a client
// round-trip an arbitrary string for the signal, which is reflected back into
// the page-level data-signals declaration on the response.
type nameComp struct{ name via.Signal[string] }

func (c *nameComp) Noop(*via.Ctx) {}
func (c *nameComp) View() h.H {
	return h.Div(h.Button(via.OnClick(c.Noop), h.Str("x")), c.name.Node())
}

// A string signal value is attacker-influenced — it round-trips through the
// client. The data-signals declaration sits inside a single-quoted attribute,
// so a raw apostrophe in the value would close the attribute early and let the
// attacker graft a live data-on-load Datastar expression onto #root (XSS). A
// hydrated apostrophe must come back entity-encoded, never raw — asserted here
// against the real HTTP response, not the internal serializer.
func TestStringSignalCannotBreakOutOfDataSignalsAttribute(t *testing.T) {
	// Echo the breakout payload back as the s0 value; the request shape (one
	// signal slot, s0) matches what the GET page declares, so dispatch proceeds.
	payload := `{"s0":"' data-on-load='alert(document.cookie)"}`
	_, body := do(t, serve(t, nameComp{}), http.MethodPost, "/_via/a/0", payload)

	if strings.Contains(body, `' data-on-load='`) {
		t.Fatalf("raw apostrophe survived into the response — attribute breakout possible\n---response---\n%s", body)
	}
	if !strings.Contains(body, "&#39;") {
		t.Fatalf("apostrophe was not entity-encoded\n---response---\n%s", body)
	}
}
