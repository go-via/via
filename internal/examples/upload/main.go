// Upload demonstrates a typed via.File field driving a real
// multipart/form-data POST. The browser uses a plain <form> (Datastar
// @post sends JSON, which can't carry file bytes) so the action runs
// over multipart and the via.File handle is bound for the duration.
//
//	go run ./internal/examples/upload
//	open http://localhost:3000
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// LastUpload survives the post-upload redirect because it's session-
// scoped. via.StateTab[T] would be lost when the redirected GET allocates
// a fresh tab + composition.
type LastUpload struct {
	Name string
	Size int64
}

type Page struct {
	Avatar via.File `via:"avatar"`
	Last   via.StateSess[LastUpload]
}

func (p *Page) Upload(ctx *via.Ctx) error {
	if p.Avatar.Present() {
		dir := filepath.Join(os.TempDir(), "via-upload-demo")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		// Server-controlled name; never trust the client filename.
		var nonce [8]byte
		_, _ = rand.Read(nonce[:])
		out := filepath.Join(dir, hex.EncodeToString(nonce[:])+filepath.Ext(p.Avatar.Filename()))
		if err := p.Avatar.Save(out); err != nil {
			return err
		}
		next := LastUpload{Name: p.Avatar.Filename(), Size: p.Avatar.Size()}
		p.Last.Update(ctx, func(LastUpload) LastUpload { return next })
	}
	// Plain <form> submit: the response body of /_action/Upload reaches
	// the browser as the new page. Redirect back to "/" so the user
	// lands on the (now refreshed) view.
	http.Redirect(ctx.Writer(), ctx.Request(), "/", http.StatusSeeOther)
	return nil
}

func (p *Page) View(ctx *via.Ctx) h.H {
	last := p.Last.Get(ctx)
	return h.Body(
		h.Main(h.Style("font-family:sans-serif;max-width:480px;margin:2rem auto;padding:0 1rem"),
			h.H1(h.Text("Upload demo")),
			h.P(h.Text("Pick a file. Saved under ")),
			h.Code(h.Text(filepath.Join(os.TempDir(), "via-upload-demo"))),

			// Plain HTML form posting multipart to the action URL.
			// No Datastar @post here — file bytes can't ride a JSON body.
			h.Form(
				h.Attr("method", "POST"),
				h.Attr("action", "/_action/Upload"),
				h.Attr("enctype", "multipart/form-data"),
				h.Style("margin-top:1rem;display:flex;flex-direction:column;gap:0.5rem"),

				// via_tab links the multipart POST back to the bound Ctx.
				h.Input(h.Type("hidden"), h.Name("via_tab"), h.Value(ctx.ID())),

				h.Input(h.Type("file"), h.Name("avatar"), h.Attr("required")),
				h.Button(h.Type("submit"), h.Text("Upload")),
			),

			h.If(last.Name != "", h.P(
				h.Strong(h.Text("Last upload: ")),
				h.Text(fmt.Sprintf("%s (%d bytes)", last.Name, last.Size)),
			)),
		),
	)
}

func main() {
	app := via.New(via.WithTitle("Via Upload"))
	via.Mount[Page](app, "/")
	_ = http.ListenAndServe(":3000", app)
}
