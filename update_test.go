package via_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/scope"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
)

type updatablePage struct {
	N      via.State[int]
	Step   via.Signal[int] `via:"step,init=1"`
	Theme  scope.User[string]
	Visits scope.App[int]
}

func (p *updatablePage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestUpdate_StateApplyFn(t *testing.T) {
	t.Parallel()

	c := &updatablePage{}
	ctx := viatest.NewCtx(t, c)

	c.N.Set(ctx, 5)
	c.N.Update(ctx, func(n int) int { return n * 2 })
	assert.Equal(t, 10, c.N.Get(ctx))
}

func TestUpdate_SignalApplyFn(t *testing.T) {
	t.Parallel()

	c := &updatablePage{}
	ctx := viatest.NewCtx(t, c)

	c.Step.Update(ctx, func(n int) int { return n + 4 })
	assert.Equal(t, 5, c.Step.Get(ctx),
		"init=1 plus +4 from Update = 5")
}

func TestUpdate_ScopeUserApplyFn(t *testing.T) {
	t.Parallel()

	c := &updatablePage{}
	ctx := viatest.NewCtx(t, c)

	c.Theme.Set(ctx, "blue")
	c.Theme.Update(ctx, func(s string) string { return s + "-dark" })
	assert.Equal(t, "blue-dark", c.Theme.Get(ctx))
}

func TestUpdate_NilFnIsNoOp(t *testing.T) {
	t.Parallel()

	c := &updatablePage{}
	ctx := viatest.NewCtx(t, c)
	c.N.Set(ctx, 7)
	c.N.Update(ctx, nil)
	assert.Equal(t, 7, c.N.Get(ctx))
}
