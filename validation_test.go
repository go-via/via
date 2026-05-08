package via_test

import (
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

type wrongView struct{}

func (w *wrongView) View() h.H { return h.Div() } // missing ctx

type initWrongReturn struct{}

func (i *initWrongReturn) Init(ctx *via.Ctx)     {} // missing error
func (i *initWrongReturn) View(ctx *via.Ctx) h.H { return h.Div() }

type disposeWrongArg struct{}

func (d *disposeWrongArg) Dispose()              {} // missing ctx
func (d *disposeWrongArg) View(ctx *via.Ctx) h.H { return h.Div() }

func TestMount_panicMessageNamesTheTypeOnMissingView(t *testing.T) {
	t.Parallel()

	type bare struct{}
	app := via.New()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		msg, _ := r.(string)
		assert.Contains(t, msg, "via.Mount")
		assert.Contains(t, msg, "View")
	}()
	via.Mount[bare](app, "/")
}

func TestMount_panicMessageOnViewWithWrongSignature(t *testing.T) {
	t.Parallel()

	app := via.New()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		msg, _ := r.(string)
		assert.True(t, strings.Contains(msg, "View has the wrong signature") ||
			strings.Contains(msg, "must implement"))
	}()
	via.Mount[wrongView](app, "/")
}

func TestMount_panicMessageOnInitWithWrongReturn(t *testing.T) {
	t.Parallel()

	app := via.New()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		msg, _ := r.(string)
		assert.Contains(t, msg, "Init has the wrong signature")
	}()
	via.Mount[initWrongReturn](app, "/")
}

func TestMount_panicMessageOnDisposeWithoutCtx(t *testing.T) {
	t.Parallel()

	app := via.New()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		msg, _ := r.(string)
		assert.Contains(t, msg, "Dispose has the wrong signature")
	}()
	via.Mount[disposeWrongArg](app, "/")
}
