package content_test

import (
	"net/http"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/demos"
)

type allowAll struct{}

func (allowAll) Allow(*via.Ctx) bool { return true }

func TestTutorialChat_clearsAWhitespaceOnlyDraft(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(demos.NewTutorialChat(allowAll{})))
	conn := app.Connect()

	status, _ := app.Action(0).Over(conn).Body(`{"draft":"   "}`).Fire()
	require.Equal(t, http.StatusNoContent, status)
	conn.Await(`"draft":""`)
}
