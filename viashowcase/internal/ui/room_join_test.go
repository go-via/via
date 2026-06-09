package ui_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/go-via/viashowcase/internal/store"
	"github.com/go-via/viashowcase/internal/ui"
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
