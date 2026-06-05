package ui

import (
	"context"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/echarts"
	"github.com/go-via/via/plugins/maplibre"
	"github.com/go-via/viashowcase/internal/core"
	"github.com/go-via/viashowcase/internal/store"
)

// DataStore is the persistence surface the UI depends on. Declaring it here (an
// interface where it is consumed, the Go idiom) lets DB-backed views be
// unit-tested with a fake; *store.Store is the production implementation.
type DataStore interface {
	CreateUser(ctx context.Context, email, passHash, display string) (store.User, error)
	UserByEmail(ctx context.Context, email string) (store.User, string, error)
	SetDisplay(ctx context.Context, userID, display string) error
	SetAvatar(ctx context.Context, userID, contentType string, data []byte) error
	SetPref(ctx context.Context, userID, theme, mode string) error
	Pref(ctx context.Context, userID string) (theme, mode string, err error)
	CreateRoom(ctx context.Context, r store.Room) error
	RoomByCode(ctx context.Context, code string) (store.Room, error)
	RoomsByHost(ctx context.Context, hostID string) ([]store.Room, error)
	SaveVote(ctx context.Context, offset int64, room, choice, by string) error
}

// Deps holds pod-local handles, set by main before Mount.
var Deps struct {
	DB    DataStore
	App   *via.App
	Chart *echarts.Chart
	Map   *maplibre.Map
}

// The app-global backplane surface. via's field walker only binds handles that
// are DIRECT fields of a composition (it recurses into child compositions, not
// plain embedded structs), so these four handles are declared field-for-field on
// each composition that uses them via the roomState macro below. Wire keys are
// global; the room Code inside each event discriminates rooms.
//
// NOTE: Go has no field mixins, so Host/Join/Persistence each declare the same
// `via:` tags — that identical tagging is exactly what makes them share one log.

// Persistence is a headless composition mounted by main solely to bind the
// app-global Votes log and register the durable OnEvent consumer. OnEvent only
// works on a runtime-bound handle (binding is internal to via), and registration
// is idempotent per (name,key), so doing it from a lifecycle hook is correct.
type Persistence struct {
	Votes via.StateAppEvents[core.Vote, core.Tallies] `via:"votes"`
}

func (p *Persistence) OnInit(_ *via.Ctx) error {
	p.Votes.OnEvent("persist-votes", func(ctx context.Context, ev core.Vote, off via.Offset) error {
		return Deps.DB.SaveVote(ctx, int64(off), ev.Room, ev.Choice, ev.By)
	})
	return nil
}

func (p *Persistence) View(_ *via.CtxR) h.H { return h.Fragment() }
