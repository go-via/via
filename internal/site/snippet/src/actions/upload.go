package actions

import (
	"io"
	"net/http"
	"os"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// snippet:start router
func newRouter() *via.Router {
	return via.NewRouter(
		via.WithMaxUpload(20<<20), // whole body; over it is 413
		via.WithMaxBody(1<<20),    // held in RAM; the rest spills to disk
	)
}

// snippet:end

type Avatar struct{ msg string }

// snippet:start upload
func (a *Avatar) Save(ctx *via.Ctx) {
	f, _, err := ctx.Request().FormFile("avatar")
	if err != nil {
		a.msg = "Pick a file."
		return
	}
	defer f.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	if http.DetectContentType(head[:n]) != "image/png" {
		a.msg = "PNG only."
		return
	}
	// The name is yours, never the client's header.Filename.
	dst, err := os.CreateTemp("/var/uploads", "avatar-*.png")
	if err != nil {
		a.msg = "Could not save."
		return
	}
	defer dst.Close()
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		a.msg = "Could not save."
		return
	}
	if _, err := io.Copy(dst, f); err != nil {
		a.msg = "Could not save."
		return
	}
	ctx.Redirect("/profile")
}

func (a *Avatar) View() h.H {
	return via.PostForm(a.Save,
		h.Input(h.Type("file"), h.Name("avatar"), h.Accept("image/png")),
		h.Button(h.Type("submit"), h.Str("Upload")),
		h.P(h.Str(a.msg)),
	)
}

// snippet:end
