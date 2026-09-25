package actions

import (
	"context"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

type User struct{ Name string }

type Store interface {
	AddReply(ctx context.Context, thread int, author, text string) error
}

type Thread struct {
	store Store
	Draft via.Signal[string]
	Error via.Signal[string]
}

// snippet:start body
func (t *Thread) Reply(ctx *via.Ctx) {
	user, ok := ctx.Session().Get[User]()
	if !ok {
		ctx.Redirect("/login")
		return
	}
	id := ctx.Param[int]("id") // mounted at /thread/{id}
	text := strings.TrimSpace(t.Draft.Get())
	if text == "" {
		t.Error.Set("Write something first.")
		return
	}
	if err := t.store.AddReply(ctx.Context(), id, user.Name, text); err != nil {
		t.Error.Set("Could not save the reply.")
		return
	}
	t.Draft.Set("")
	t.Error.Set("")
}

// snippet:end

func (t *Thread) View() h.H {
	return h.Form(on.Submit(t.Reply),
		h.Input(t.Draft.Bind()),
		h.Small(t.Error.Display()),
		h.Button(h.Type("submit"), h.Str("Reply")),
	)
}
