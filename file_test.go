package via_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type uploadPage struct {
	Avatar via.File           `via:"avatar"`
	SaveTo via.Signal[string] `via:"saveTo"` // test-supplied destination dir
}

func (p *uploadPage) Upload(ctx *via.Ctx) error {
	if !p.Avatar.Present() {
		return nil
	}
	dir := p.SaveTo.Get(ctx)
	if dir == "" {
		return nil
	}
	return p.Avatar.Save(filepath.Join(dir, "out.bin"))
}

func (p *uploadPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestFile_typedFieldPopulatedFromMultipartUpload(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[uploadPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	dir := t.TempDir()

	body := []byte("PNG-bytes-pretend")
	require.Equal(t, http.StatusOK,
		tc.Action("Upload").
			WithFile("avatar", "me.png", body).
			WithSignal("saveTo", dir).
			Fire())

	got, err := os.ReadFile(filepath.Join(dir, "out.bin"))
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

type readMultipartPage struct{}

func (p *readMultipartPage) Read(ctx *via.Ctx) error {
	mr, err := ctx.MultipartReader()
	if err != nil {
		return err
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, part)
		part.Close()
	}
}

func (p *readMultipartPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestMultipartReader_streamsRawParts(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[readMultipartPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")

	require.Equal(t, http.StatusOK,
		tc.Action("Read").
			WithFile("blob", "x.bin", []byte("zzz")).
			WithSignal("a", "1").
			WithSignal("b", "2").
			Fire())
}

type bytesEchoPage struct {
	Blob   via.File       `via:"blob"`
	Length via.State[int] `via:"length"`
}

func (p *bytesEchoPage) Read(ctx *via.Ctx) error {
	b, err := p.Blob.Bytes()
	if err != nil {
		return err
	}
	p.Length.Set(ctx, len(b))
	return nil
}

func (p *bytesEchoPage) View(ctx *via.Ctx) h.H { return h.Div(p.Length.Text()) }

func TestFile_Bytes_readsMultipartContent(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[bytesEchoPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	payload := []byte("hello-from-Bytes")
	require.Equal(t, http.StatusOK,
		tc.Action("Read").
			WithFile("blob", "x.bin", payload).
			Fire())

	frames, cancel := tc.SSE()
	defer cancel()
	viatest.AwaitFrame(t, frames, 2*time.Second, ">16<")
}

func TestFile_oversizedRequestReturns413(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithMaxUploadSize(64), // tiny cap for the multipart path
	)
	via.Mount[uploadPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")

	got := tc.Action("Upload").
		WithFile("avatar", "big.bin", bytes.Repeat([]byte("X"), 4096)).
		Fire()
	assert.Equal(t, http.StatusRequestEntityTooLarge, got)
}
