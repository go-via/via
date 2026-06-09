package via_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// addItem is the immutable fact; the projected value is the list of texts in
// append order. Fold is pure and copies acc (determinism contract).
type addItem struct{ Text string }

func (addItem) Fold(acc []string, ev addItem) []string {
	return append(append([]string(nil), acc...), ev.Text)
}

type feedPage struct {
	Items via.StateAppEvents[addItem, []string]
}

func (p *feedPage) Add(ctx *via.Ctx) {
	_, _ = p.Items.Append(ctx, addItem{Text: "hello"})
}

func (p *feedPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.ID("feed"), h.Text(strings.Join(p.Items.Read(ctx), ",")))
}

// An empty log projects to the Go zero of V (no Zero() method, no genesis
// event): a freshly-loaded feed with no appends yet must render the empty
// projection, not a nil-deref or a missing node.
func TestStateAppEvents_readProjectsZeroValueBeforeAnyAppend(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[feedPage](app, "/")
	defer server.Close()

	c := vt.NewClient(t, server, "/")
	assert.Contains(t, c.HTML(), `<div id="feed"></div>`,
		"an empty event log projects to the zero value of V")
}

// The whole point of StateAppEvents: appending a fact in one tab's action must
// surface — folded into the projected value — in every other live tab that read
// the key. If the projector or the broadcast were missing, the appended event
// would never reach a peer's render.
func TestStateAppEvents_appendedEventFoldsAndReachesALiveSubscriber(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[feedPage](app, "/")
	defer server.Close()

	a := vt.NewClient(t, server, "/")
	b := vt.NewClient(t, server, "/")

	framesB, cancelB := b.SSEReady()
	defer cancelB()

	require.Equal(t, 200, a.Action("Add").Fire())
	vt.AwaitFrame(t, framesB, 2*time.Second, `<div id="feed">hello</div>`)
}

// The projection is app-scoped: it lives in the backplane, not the writing tab.
// A brand-new session loading the page must see the already-folded value in its
// very first render — proving the value outlives the tab that appended it.
func TestStateAppEvents_projectionIsAppScopedAndOutlivesTheWriter(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[feedPage](app, "/")
	defer server.Close()

	a := vt.NewClient(t, server, "/")
	require.Equal(t, 200, a.Action("Add").Fire())
	require.Equal(t, 200, a.Action("Add").Fire())

	// A fresh client (different session) must see both folded items. The
	// projector folds asynchronously, so allow it to catch up.
	require.Eventually(t, func() bool {
		fresh := vt.NewClient(t, server, "/")
		return strings.Contains(fresh.HTML(), `<div id="feed">hello,hello</div>`)
	}, 2*time.Second, 20*time.Millisecond,
		"a fresh session must see the app-scoped folded projection")
}

// Text is the v1 sibling of StateApp.Text: it renders the projected value as a
// text node, so a View can drop the projection in without calling Read + a
// formatter by hand.
type textFeedPage struct {
	Items via.StateAppEvents[addItem, []string]
}

func (p *textFeedPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.ID("t"), p.Items.Text(ctx))
}

func TestStateAppEvents_textRendersTheProjectedValue(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[textFeedPage](app, "/")
	defer server.Close()

	c := vt.NewClient(t, server, "/")
	assert.Contains(t, c.HTML(), `<div id="t">[]</div>`,
		"Text renders the projected value (empty []string formats as [])")
}

// Append is reachable only from a via_tab + session-gated action ctx; a nil ctx
// means the call did not come from a legitimate tab action, so it must panic
// rather than silently mutate shared state (parity with StateApp.Update).
func TestStateAppEvents_appendPanicsOnNilCtx(t *testing.T) {
	t.Parallel()
	var l via.StateAppEvents[addItem, []string]
	assert.PanicsWithValue(t,
		"via: StateAppEvents.Append called with nil *Ctx",
		func() { _, _ = l.Append(nil, addItem{Text: "x"}) },
	)
}
