package live_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"go-via.dev/site/snippet/src/live"
)

func TestWall_followsTheBoardOnEveryTab(t *testing.T) {
	t.Parallel()
	board := live.NewBoard()
	board.Add("first")
	app := vt.Serve(t, via.Handler(live.Wall{Board: board}, via.WithLogger(vt.Logger(t))))
	alice, bob := app.Connect(), app.Connect()

	board.Add("second")
	alice.Await("<li>first</li><li>second</li>")
	bob.Await("<li>first</li><li>second</li>")
}
