package testing

import (
	"sync/atomic"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type Stepper struct {
	n    *atomic.Int64
	Step via.Signal[int] `via:"init=1"`
}

func (s *Stepper) Add(ctx *via.Ctx) { s.n.Add(int64(s.Step.Get())) }

func (s *Stepper) View() h.H {
	return h.Div(
		h.Input(h.Type("number"), s.Step.Bind()),
		h.Button(on.Click(s.Add), h.Str("add")),
		h.P(h.Str("total: "), h.Str(s.n.Load())),
	)
}

// snippet:start body
func TestStepper_addsThePostedStep(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(Stepper{n: new(atomic.Int64)}))

	status, body := app.Action(0).Body(`{"step":5}`).Fire()
	require.Equal(t, 200, status)
	assert.Contains(t, body, "total: 5")
}

// snippet:end

type Shelf struct {
	titles []string
	Picked via.Signal[string]
}

func (s *Shelf) Pick(ctx *via.Ctx, i int) { s.Picked.Set(s.titles[i]) }

func (s *Shelf) View() h.H {
	var rows []h.H
	for i, title := range s.titles {
		rows = append(rows, h.Li(h.Button(on.Click(on.WithArg(s.Pick, i)), h.Str(title))))
	}
	return h.Div(h.Ul(rows...), h.P(s.Picked.Display()))
}

// snippet:start arg
func TestShelf_eachRowCarriesItsOwnArg(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(Shelf{titles: []string{"Solaris", "Dune", "Ubik"}}))

	_, body := app.Action(1).Fire() // the second row's button
	assert.Contains(t, body, `"picked":"Dune"`)
}

// snippet:end

// snippet:start child
type Search struct{ Q via.Signal[string] }

func (s *Search) Go(ctx *via.Ctx) { s.Q.Set("searched: " + s.Q.Get()) }

func (s *Search) View() h.H {
	return h.Form(on.Submit(s.Go), h.Input(s.Q.Bind()), h.Button(h.Str("go")))
}

type Layout struct{ Search Search }

func (l *Layout) View() h.H { return h.Main(via.Child(l.Search)) }

func TestLayout_searchChild(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(Layout{}))

	// "0" is the root's first Child; its signals are prefixed with its field name.
	_, body := app.ChildAction("0", 0).Body(`{"search__q":"via"}`).Fire()
	assert.Contains(t, body, "searched: via")
}

// snippet:end

// snippet:start origin
func TestCounter_refusesCrossSitePosts(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(Counter{n: new(atomic.Int64)},
		via.WithTrustedOrigin("https://example.com")))

	status, _ := app.Action(1).Fire() // same-origin by default
	assert.Equal(t, 200, status)

	status, _ = app.Action(1).Origin("https://evil.example").Fire()
	assert.Equal(t, 403, status)

	status, _ = app.Action(1).NoOrigin().Fire()
	assert.Equal(t, 403, status)
}

// snippet:end

// snippet:start tls
func TestCounter_overTLSRefusesAnHTTPOrigin(t *testing.T) {
	t.Parallel()
	app := vt.ServeTLS(t, via.Handler(Counter{n: new(atomic.Int64)},
		via.WithTrustedOrigin("https://example.com")))

	status, _ := app.Action(1).Host("app.example").Origin("https://app.example").Fire()
	assert.Equal(t, 200, status)

	status, _ = app.Action(1).Host("app.example").Origin("http://app.example").Fire()
	assert.Equal(t, 403, status) // a scheme downgrade on a TLS request
}

// snippet:end
