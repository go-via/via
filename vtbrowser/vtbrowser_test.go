package vtbrowser_test

import (
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vtbrowser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type counter struct {
	Hits via.StateTabNum[int]
}

func (c *counter) Inc(ctx *via.Ctx) { c.Hits.Op(ctx).Add(1) }

func (c *counter) View(ctx *via.CtxR) h.H {
	// The top margin keeps the button out from under via's fixed-position
	// reconnect banner, so a real click during an outage lands on the
	// button instead of the overlay.
	return h.Main(
		h.Style("margin-top:6rem"),
		h.Span(h.ID("hits"), c.Hits.Text(ctx)),
		h.Button(h.ID("inc"), h.Text("inc"), on.Click(c.Inc)),
	)
}

type editor struct {
	Saves via.StateTabNum[int]
	Name  via.Signal[string]
}

func (c *editor) Save(ctx *via.Ctx) { c.Saves.Op(ctx).Add(1) }

func (c *editor) View(ctx *via.CtxR) h.H {
	return h.Main(
		h.Span(h.ID("saves"), c.Saves.Text(ctx)),
		h.Input(h.ID("name"), c.Name.Bind(), on.Key("Enter", c.Save)),
	)
}

// Insecure cookies because httptest serves plain http; the session
// cookie must ride it for the SSE stream to authorise.
func newApp() *via.App { return via.New(via.WithInsecureCookies()) }

func TestBrowser_clickIncrementsCounter(t *testing.T) {
	app := newApp()
	via.Mount[counter](app, "/")
	s := vtbrowser.Open(t, app)

	s.WaitText("#hits", "0")
	s.Click("#inc")
	s.WaitText("#hits", "1")
	assert.Empty(t, s.ConsoleErrors())
}

func TestBrowser_morphPreservesFocusedInputAcrossPatch(t *testing.T) {
	app := newApp()
	via.Mount[editor](app, "/")
	s := vtbrowser.Open(t, app)

	s.WaitText("#saves", "0")
	s.Type("#name", "alice")
	// Enter fires the Save action while focus stays inside the input, so
	// the SSE patch that re-renders the view morphs around a focused node.
	s.Type("#name", "\r")
	s.WaitText("#saves", "1")

	var focusedID, value string
	s.Eval(`document.activeElement ? document.activeElement.id : ""`, &focusedID)
	s.Eval(`document.querySelector("#name").value`, &value)
	assert.Equal(t, "name", focusedID,
		"focused input must keep focus across the morph")
	assert.Equal(t, "alice", value,
		"typed value must survive the morph")
	assert.Empty(t, s.ConsoleErrors())
}

func TestBrowser_reconnectBannerClearsOnResume(t *testing.T) {
	app := newApp()
	via.Mount[counter](app, "/")
	s := vtbrowser.Open(t, app)

	s.WaitText("#hits", "0")

	// Going offline alone may not sever an already-established stream, and
	// severing alone may reconnect faster than the banner can be observed —
	// doing both makes the outage long enough to assert on deterministically.
	s.SetOffline(true)
	s.Server().CloseClientConnections()

	bannerVisible := func() bool {
		var visible bool
		s.Eval(`(function(){var b=document.querySelector('#via-reconnect-banner');`+
			`return !!b && b.style.display !== 'none'})()`, &visible)
		return visible
	}
	require.Eventually(t, bannerVisible, 15*time.Second, 100*time.Millisecond,
		"reconnect banner must appear after the SSE stream drops")

	s.SetOffline(false)
	// No interaction past this point: the resumed stream's re-bootstrap
	// patch must clear the banner on its own. A stuck banner overlays the
	// page and swallows clicks, so passive clearing is the contract.
	require.Eventually(t, func() bool { return !bannerVisible() },
		15*time.Second, 100*time.Millisecond,
		"reconnect banner must clear passively once the stream resumes")
	s.Click("#inc")
	s.WaitText("#hits", "1")
	assert.Empty(t, s.ConsoleErrors())
}
