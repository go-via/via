package ui_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/go-via/viashowcase/internal/core"
	"github.com/go-via/viashowcase/internal/store"
	"github.com/go-via/viashowcase/internal/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDB is an in-memory ui.DataStore for hermetic view tests (no Postgres). Only
// RoomByCode is meaningful here; the rest satisfy the interface as no-ops.
type fakeDB struct{ rooms map[string]store.Room }

func (f fakeDB) RoomByCode(_ context.Context, code string) (store.Room, error) {
	if r, ok := f.rooms[code]; ok {
		return r, nil
	}
	return store.Room{}, store.ErrNotFound
}

func (fakeDB) CreateUser(context.Context, string, string, string) (store.User, error) {
	return store.User{}, nil
}
func (fakeDB) UserByEmail(context.Context, string) (store.User, string, error) {
	return store.User{}, "", nil
}
func (fakeDB) SetDisplay(context.Context, string, string) error              { return nil }
func (fakeDB) SetAvatar(context.Context, string, string, []byte) error       { return nil }
func (fakeDB) SetPref(context.Context, string, string, string) error         { return nil }
func (fakeDB) Pref(context.Context, string) (string, string, error)          { return "", "", nil }
func (fakeDB) CreateRoom(context.Context, store.Room) error                  { return nil }
func (fakeDB) RoomsByHost(context.Context, string) ([]store.Room, error)     { return nil, nil }
func (fakeDB) SaveVote(context.Context, int64, string, string, string) error { return nil }

// withFakeDB installs a fake store for the duration of one (non-parallel) test.
func withFakeDB(t *testing.T, f fakeDB) {
	prev := ui.Deps.DB
	ui.Deps.DB = f
	t.Cleanup(func() { ui.Deps.DB = prev })
}

func mountJoin(t *testing.T) *httptest.Server {
	t.Helper()
	app := via.New(via.WithBackplane(via.InMemory()))
	srv := vt.Serve(t, app)
	via.Mount[ui.Join](app, "/r/{code}")
	return srv
}

// A stale or mistyped share link must render a clear "room not found" state, not
// a broken half-page with a stray nickname field — the fix made in this app.
func TestJoinShowsFriendlyNotFoundForUnknownCode(t *testing.T) {
	withFakeDB(t, fakeDB{rooms: map[string]store.Room{}})
	html := vt.NewClient(t, mountJoin(t), "/r/nope").HTML()
	require.Contains(t, html, "Room not found")
	require.NotContains(t, html, `data-bind="nick"`,
		"the not-found page must not show the participation controls")
}

// tallyReader is a test-only observation point: it reads the same shared
// "votes" event log Join writes to and renders one room's tally as plain
// "choice=count" spans, so a test can assert what Join.Vote actually
// recorded — without depending on the host view (which is auth-gated).
type tallyReader struct {
	Votes via.StateAppEvents[core.Vote, core.Tallies] `via:"votes"`
	Code  string                                      `path:"code"`
}

func (r *tallyReader) View(ctx *via.CtxR) h.H {
	t := r.Votes.Read(ctx).For(r.Code)
	items := make([]h.H, 0, len(t))
	for choice, n := range t {
		items = append(items, h.Span(h.Textf("%s=%d", choice, n)))
	}
	return h.Div(items...)
}

// roomVoteFixture mounts Join (the writer) and tallyReader (the observer) on
// one app sharing a backplane, for a room of the given kind/choices, and
// returns clients for casting a vote and reading the resulting tally. These
// tests can't run in parallel: withFakeDB swaps the package-global ui.Deps.DB.
func roomVoteFixture(t *testing.T, kind string, choices []string) (vote, tally *vt.Client) {
	t.Helper()
	withFakeDB(t, fakeDB{rooms: map[string]store.Room{
		"good": {Code: "good", Title: "Lunch?", Kind: kind, Choices: choices},
	}})
	app := via.New(via.WithBackplane(via.InMemory()))
	srv := vt.Serve(t, app)
	via.Mount[ui.Join](app, "/r/{code}")
	via.Mount[tallyReader](app, "/tally/{code}")
	return vt.NewClient(t, srv, "/r/good"), vt.NewClient(t, srv, "/tally/good")
}

// A choice that is not on the poll ballot must never enter the tally, while a
// legitimate ballot choice cast alongside it still counts: the vote buttons
// are bypassable (the choice rides on a client-set signal), so a crafted
// action with an off-ballot choice would otherwise pollute results. Asserting
// the valid vote lands proves the guard is selective, not a blanket reject.
func TestOffBallotChoiceIsRejectedButValidVoteCountsForPollRooms(t *testing.T) {
	vote, tally := roomVoteFixture(t, "poll", []string{"Pizza", "Sushi"})

	// The off-ballot vote is fired first; once the later on-ballot vote folds,
	// the off-ballot one would have folded too if it were ever going to — so a
	// single post-settle check is deterministic (no background polling, which
	// would race the test's server teardown via the Fatal-on-error Reload).
	require.Equal(t, http.StatusOK, vote.Action("Vote").WithSignal("draft", "Tacos").Fire())
	require.Equal(t, http.StatusOK, vote.Action("Vote").WithSignal("draft", "Pizza").Fire())

	require.Eventually(t, func() bool {
		return strings.Contains(tally.Reload(), "Pizza=1")
	}, 2*time.Second, 20*time.Millisecond,
		"an on-ballot vote must fold into the tally")
	assert.NotContains(t, tally.Reload(), "Tacos",
		"an off-ballot choice must not appear in the poll tally")
}

// An empty submission (whitespace-only or blank) is a no-op: it must never
// produce a phantom "" tally entry.
func TestEmptyChoiceIsIgnored(t *testing.T) {
	vote, tally := roomVoteFixture(t, "poll", []string{"Pizza", "Sushi"})

	// Fire the blank first, then a real vote as a settle gate; a phantom
	// empty-choice entry renders as "<span>=N</span>" (a real choice renders
	// "<span>Choice=N</span>"), so it never collides with the gate.
	require.Equal(t, http.StatusOK, vote.Action("Vote").WithSignal("draft", "   ").Fire())
	require.Equal(t, http.StatusOK, vote.Action("Vote").WithSignal("draft", "Pizza").Fire())

	require.Eventually(t, func() bool {
		return strings.Contains(tally.Reload(), "Pizza=1")
	}, 2*time.Second, 20*time.Millisecond,
		"the gate vote must fold into the tally")
	assert.NotContains(t, tally.Reload(), "<span>=",
		"a blank submission must not record any vote")
}

// Word-cloud rooms accept free text by design, so the poll-ballot guard must
// not touch them — any submitted word must still be recorded.
func TestFreeTextIsRecordedForCloudRooms(t *testing.T) {
	// A cloud room has no fixed Choices; the off-ballot guard, if applied here,
	// would reject every word.
	vote, tally := roomVoteFixture(t, "cloud", nil)

	require.Equal(t, http.StatusOK, vote.Action("Vote").WithSignal("draft", "kubernetes").Fire())

	require.Eventually(t, func() bool {
		return strings.Contains(tally.Reload(), "kubernetes=1")
	}, 2*time.Second, 20*time.Millisecond,
		"a word-cloud entry must fold into the tally")
}

// A valid poll link must render each choice as a vote control.
func TestJoinRendersPollChoicesForValidRoom(t *testing.T) {
	withFakeDB(t, fakeDB{rooms: map[string]store.Room{
		"good": {Code: "good", Title: "Lunch?", Kind: "poll", Choices: []string{"Pizza", "Sushi"}},
	}})
	html := vt.NewClient(t, mountJoin(t), "/r/good").HTML()
	require.Contains(t, html, "Pizza")
	require.Contains(t, html, "Sushi")
	require.NotContains(t, html, "Room not found")
}
