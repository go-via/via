package via_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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

func TestFile_filePresentFalseWhenNoUpload(t *testing.T) {
	t.Parallel()

	c := &uploadPage{}
	ctx := viatest.NewCtx(t, c)
	assert.False(t, c.Avatar.Present(),
		"unbound File handle reports Present() == false")
	assert.Equal(t, "", c.Avatar.Filename())
	assert.Equal(t, int64(0), c.Avatar.Size())
	assert.Equal(t, "", c.Avatar.ContentType())
	_, err := c.Avatar.Open()
	assert.Error(t, err, "Open with no upload should error")
	assert.Equal(t, "avatar", c.Avatar.Key(),
		"Key() must be set at Ctx construction, not only after a multipart action")

	// no panic when ctx has no in-flight request
	_, err = ctx.MultipartReader()
	assert.Error(t, err)
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

func TestFile_oversizedRequestReturns413(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithMaxRequestBody(64), // tiny cap
	)
	via.Mount[uploadPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")

	got := tc.Action("Upload").
		WithFile("avatar", "big.bin", bytes.Repeat([]byte("X"), 4096)).
		Fire()
	assert.Equal(t, http.StatusRequestEntityTooLarge, got)
}
