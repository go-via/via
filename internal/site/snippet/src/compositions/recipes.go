package compositions

import (
	"context"
	"net/http"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// snippet:start lift
// Cart is the mounted root.
type Cart struct {
	Items via.List[string]
	Total Total
}

type Total struct{ N int }

func (c *Cart) Add(ctx *via.Ctx) {
	c.Items.Append("apple")
	// the push copies Total with N
	c.Total.N = len(c.Items.Get())
}

func (c *Cart) View() h.H {
	return h.Div(
		h.Button(on.Click(c.Add),
			h.Str("add")),
		h.Ul(c.Items.Each(c.row)),
		via.Child(c.Total),
	)
}

// snippet:end

func (c *Cart) row(s string) h.H { return h.Li(h.Str(s)) }
func (t *Total) View() h.H       { return h.P(h.Str("items: "), h.Str(t.N)) }

type userKey struct{}

// WithUser is plain net/http middleware around the Router.
func WithUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), userKey{}, r.Header.Get("X-User"))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// snippet:start context
type Badge struct{ User string }

func (b *Badge) OnInit(
	ctx *via.Ctx,
) error {
	// set by the WithUser middleware
	r := ctx.Request()
	b.User, _ = r.Context().
		Value(userKey{}).(string)
	return nil
}

// snippet:end

func (b *Badge) View() h.H { return h.Span(h.Str(b.User)) }

// snippet:start cond
type Menu struct {
	// from the session, in OnInit
	Admin bool
	// browser-only
	Open via.SignalCS[bool]
}

func (m *Menu) View() h.H {
	open := m.Open.Ref()
	click := on.ClickCS(open.Toggle())
	return h.Nav(
		h.Button(click, h.Str("menu")),
		h.Ul(h.DataShow(open),
			h.Li(h.Str("profile")),
			via.When(m.Admin, m.item),
		),
	)
}

// snippet:end

func (m *Menu) item() h.H { return h.Li(h.Button(on.Click(m.Purge), h.Str("purge cache"))) }

func (m *Menu) Purge(ctx *via.Ctx) {}

// snippet:start derived
type Order struct {
	Qty   via.Signal[int]
	Price int
}

// Go, at render: can call anything.
func (o *Order) Subtotal() int {
	return o.Qty.Get() * o.Price
}

// JS, in the browser, per keystroke.
func (o *Order) live() expr.Expr {
	return expr.Rawf("%s * %s",
		o.Qty.Ref(),
		expr.Val(o.Price))
}

func (o *Order) View() h.H {
	return h.Div(
		h.Input(o.Qty.Bind()),
		h.Str(o.Subtotal()),
		h.Span(h.DataText(o.live())),
	)
}

// snippet:end

// snippet:start effect
type Clock struct {
	Now via.State[string]
}

func (c *Clock) OnInit(
	ctx *via.Ctx,
) error {
	// stops when the stream closes
	ctx.Tick(time.Second, c.tick)
	return nil
}

func (c *Clock) tick(ctx *via.Ctx) {
	t := time.Now().Format("15:04:05")
	c.Now.Set(t)
}

// snippet:end

func (c *Clock) View() h.H { return h.P(c.Now.Display()) }
