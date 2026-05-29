package via_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type uploadPage struct {
	Avatar via.File      `via:"avatar"`
	SaveTo via.SignalStr `via:"saveTo"` // test-supplied destination dir
}

func (p *uploadPage) Upload(ctx *via.Ctx) error {
	if !p.Avatar.Present() {
		return nil
	}
	dir := p.SaveTo.Read(ctx)
	if dir == "" {
		return nil
	}
	return p.Avatar.Save(filepath.Join(dir, "out.bin"))
}

func (p *uploadPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestFile_typedFieldPopulatedFromMultipartUpload(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[uploadPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
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

func (p *readMultipartPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestMultipartReader_streamsRawParts(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[readMultipartPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")

	require.Equal(t, http.StatusOK,
		tc.Action("Read").
			WithFile("blob", "x.bin", []byte("zzz")).
			WithSignal("a", "1").
			WithSignal("b", "2").
			Fire())
}

type bytesEchoPage struct {
	Blob   via.File             `via:"blob"`
	Length via.StateTabNum[int] `via:"length"`
}

func (p *bytesEchoPage) Read(ctx *via.Ctx) error {
	b, err := p.Blob.Bytes()
	if err != nil {
		return err
	}
	p.Length.Write(ctx, len(b))
	return nil
}

func (p *bytesEchoPage) View(ctx *via.CtxR) h.H { return h.Div(p.Length.Text(ctx)) }

func TestFile_Bytes_readsMultipartContent(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[bytesEchoPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	payload := []byte("hello-from-Bytes")
	require.Equal(t, http.StatusOK,
		tc.Action("Read").
			WithFile("blob", "x.bin", payload).
			Fire())

	frames, cancel := tc.SSE()
	defer cancel()
	vt.AwaitFrame(t, frames, 2*time.Second, ">16<")
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

	tc := vt.NewClient(t, server, "/")

	got := tc.Action("Upload").
		WithFile("avatar", "big.bin", bytes.Repeat([]byte("X"), 4096)).
		Fire()
	assert.Equal(t, http.StatusRequestEntityTooLarge, got)
}

type fileMetaPage struct {
	Doc  via.File `via:"doc"`
	Info via.StateTabStr
}

func (p *fileMetaPage) Inspect(ctx *via.Ctx) error {
	p.Info.Write(ctx, fmt.Sprintf("name=%s size=%d type=%s key=%s present=%t",
		p.Doc.Filename(), p.Doc.Size(), p.Doc.ContentType(), p.Doc.Key(), p.Doc.Present()))
	return nil
}

func (p *fileMetaPage) View(ctx *via.CtxR) h.H { return h.Div(p.Info.Text(ctx)) }

func TestFile_metadataAccessorsReflectUpload(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[fileMetaPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	// CreateFormFile labels the part application/octet-stream; Size is the
	// body length; Key is the wire field name.
	require.Equal(t, 200, tc.Action("Inspect").
		WithFile("doc", "report.pdf", []byte("hello pdf")).Fire())
	vt.AwaitFrame(t, frames, 2*time.Second,
		"name=report.pdf", "size=9", "type=application/octet-stream",
		"key=doc", "present=true")
}

func TestFile_metadataAccessorsAreZeroWhenNoUpload(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[fileMetaPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	// No file part: accessors hit their empty-handle guards and return zero
	// values, but Key (bound at Ctx construction) still reports the key.
	require.Equal(t, 200, tc.Action("Inspect").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second,
		"name= size=0 type= key=doc present=false")
}

type fileErrPage struct {
	Doc    via.File `via:"doc"`
	Result via.StateTabStr
}

func fileErrMsg(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func (p *fileErrPage) Probe(ctx *via.Ctx) error {
	_, openErr := p.Doc.Open()
	_, bytesErr := p.Doc.Bytes()
	saveErr := p.Doc.Save(filepath.Join(os.TempDir(), "via-absent-upload"))
	p.Result.Write(ctx, "open="+fileErrMsg(openErr)+
		"|bytes="+fileErrMsg(bytesErr)+"|save="+fileErrMsg(saveErr))
	return nil
}

func (p *fileErrPage) Reader(ctx *via.Ctx) error {
	_, err := ctx.MultipartReader()
	p.Result.Write(ctx, "reader="+fileErrMsg(err))
	return nil
}

func (p *fileErrPage) SaveBad(ctx *via.Ctx) error {
	// Parent directory does not exist, so os.OpenFile fails after Open
	// succeeds — Save must surface that error, not swallow it.
	err := p.Doc.Save(filepath.Join(os.TempDir(), "via-missing-dir-xyz", "out.bin"))
	p.Result.Write(ctx, "savebad="+fileErrMsg(err))
	return nil
}

func (p *fileErrPage) View(ctx *via.CtxR) h.H { return h.Div(p.Result.Text(ctx)) }

func TestFile_openBytesSaveReturnErrorWhenNoUpload(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[fileErrPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	// No file part: Open returns errNoFile and Bytes/Save propagate it
	// (Save bails before ever touching the path).
	require.Equal(t, 200, tc.Action("Probe").Fire())
	vt.AwaitFrame(t, frames, 2*time.Second,
		"open=via: no file uploaded for this field",
		"bytes=via: no file uploaded for this field",
		"save=via: no file uploaded for this field")
}

func TestMultipartReader_errorsOnNonMultipartRequest(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[fileErrPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	// A plain (non-multipart) action body makes r.MultipartReader fail.
	require.Equal(t, 200, tc.Action("Reader").Fire())
	frame := vt.AwaitFrame(t, frames, 2*time.Second, "reader=")
	assert.Contains(t, frame, "multipart",
		"MultipartReader on a non-multipart request must return an error")
}

func TestFile_saveSurfacesOpenFileError(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[fileErrPage](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	require.Equal(t, 200, tc.Action("SaveBad").
		WithFile("doc", "x.bin", []byte("data")).Fire())
	frame := vt.AwaitFrame(t, frames, 2*time.Second, "savebad=")
	assert.Contains(t, frame, "via-missing-dir-xyz",
		"Save must return the os.OpenFile error when the destination is unwritable")
}
