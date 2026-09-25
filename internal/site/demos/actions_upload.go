package demos

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Upload reads a file from a native multipart submit and keeps nothing: it
// hashes the bytes, sniffs the type and reports both. The results are plain
// fields for the same reason as Signup's: the submit is a navigation, and they
// only have to reach the page this request renders.
type Upload struct {
	// Limiter: see shared_contract.go.
	Lim    Limiter
	report []string
	err    string
}

func NewUpload(lim Limiter) Upload {
	if lim == nil {
		panic("demos: NewUpload: Upload.Lim must not be nil")
	}
	return Upload{Lim: lim}
}

func (u *Upload) Inspect(ctx *via.Ctx) {
	if !u.Lim.Allow(ctx) {
		u.err = "Slow down: this demo takes 10 uploads a minute."
		return
	}
	f, hdr, err := ctx.Request().FormFile("file")
	if err != nil {
		u.err = "Pick a file first."
		return
	}
	defer f.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	sum := sha256.New()
	sum.Write(head[:n])
	size, err := io.Copy(sum, f)
	if err != nil {
		u.err = "The upload broke off."
		return
	}
	u.report = []string{
		"name (client-supplied): " + hdr.Filename,
		"size: " + strconv.FormatInt(size+int64(n), 10) + " bytes",
		"sniffed type: " + http.DetectContentType(head[:n]),
		"claimed type: " + hdr.Header.Get("Content-Type"),
		"sha256: " + hex.EncodeToString(sum.Sum(nil))[:16] + "…",
	}
}

func (u *Upload) View() h.H {
	rows := make([]h.H, 0, len(u.report))
	for _, r := range u.report {
		rows = append(rows, h.Li(h.Str(r)))
	}
	return via.PostForm(u.Inspect,
		h.Div(h.Class("row"),
			h.Input(h.Type("file"), h.Name("file")),
			h.Button(h.Type("submit"), h.Str("Inspect")),
		),
		h.Small(h.Class("err"), h.Str(u.err)),
		h.Ul(rows...),
	)
}
