package via_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/require"
)

type formPage struct {
	Email    via.Signal[string]
	Password via.Signal[string]
	Age      via.Signal[int]
	Result   via.State[string]
}

type loginForm struct {
	Email    string `form:"email"`
	Password string `form:"password"`
	Age      int    `form:"age"`
}

func (p *formPage) Submit(ctx *via.Ctx) error {
	var f loginForm
	via.DecodeForm(ctx, &f)
	p.Result.Set(ctx, f.Email+"|"+f.Password+"|"+strings.Repeat("*", f.Age))
	return nil
}

func (p *formPage) View(ctx *via.Ctx) h.H {
	return h.Div(p.Result.Text())
}

func TestDecodeForm_readsSignalsIntoTaggedStruct(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[formPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("Submit").
		WithSignal("email", "alice@example.com").
		WithSignal("password", "secret").
		WithSignal("age", 3).Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, "alice@example.com|secret|***")
}

type formNoTag struct {
	UserName via.Signal[string]
	Captured via.State[string]
}

type lazyForm struct {
	UserName string // no tag — uses lowercased field name "userName"
}

func (p *formNoTag) Submit(ctx *via.Ctx) error {
	var f lazyForm
	via.DecodeForm(ctx, &f)
	p.Captured.Set(ctx, f.UserName)
	return nil
}

func (p *formNoTag) View(ctx *via.Ctx) h.H { return h.Div(p.Captured.Text()) }

func TestDecodeForm_defaultsKeyToLowercasedFieldName(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[formNoTag](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE()
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	require.Equal(t, 200, tc.Action("Submit").
		WithSignal("userName", "bob").Fire())
	viatest.AwaitFrame(t, frames, 2*time.Second, ">bob<")
}
