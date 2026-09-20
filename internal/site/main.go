// Command site serves go-via.dev: via's documentation, written with via, so
// every demo on it is the library actually running.
package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/content"
	"go-via.dev/site/shell"
)

//go:embed static
var staticFS embed.FS

func errorPage(ctx *via.Ctx, e via.PageError) h.H {
	switch e.Reason {
	case via.ReasonNotFound:
		return shell.Page("No such page", shell.NavItem{},
			h.P(h.Str("That URL is not part of the docs.")),
			h.P(h.A(h.Href("/"), h.Str("Back to the front page"))),
		)
	default:
		return shell.Page("Something broke", shell.NavItem{},
			h.P(h.Str(e.Detail)),
			h.P(h.A(h.Href("/"), h.Str("Back to the front page"))),
		)
	}
}

// sessionKey keeps cookies valid across restarts when it is set. A dev boot
// without it gets a throwaway key, which logs everyone out on every restart.
func sessionKey() []byte {
	if k := os.Getenv("VIA_SESSION_KEY"); k != "" {
		return []byte(k)
	}
	log.Print("VIA_SESSION_KEY is unset: signing cookies with a random key for this boot only")
	k := make([]byte, 32)
	rand.Read(k)
	return k
}

// options are the router's, with the ones that only apply in production
// appended: an unset VIA_ORIGIN must not allowlist the empty origin.
func options() []via.Option {
	opts := []via.Option{
		via.WithSessionKey(sessionKey()),
		// Router-wide: the assets are the same on every page, so no page can
		// widen its own CSP.
		via.WithHead(via.Head{
			Lang: "en",
			// Raw is emitted unparsed; WithHead panics on a script or style
			// in it, because those belong in Assets and the CSP.
			Raw: `<meta name="viewport" content="width=device-width, initial-scale=1">` +
				`<link rel="icon" type="image/svg+xml" href="/static/brand/icon-amber-ink.svg">`,
			Assets: via.Assets{
				Styles:  []via.Style{{Href: "/static/site.css"}, {Href: "/static/chroma.css"}},
				Scripts: []via.Script{{Src: "/static/inspector.js", Defer: true}},
			},
		}),
		via.WithErrorPage(errorPage),
	}
	if o := os.Getenv("VIA_ORIGIN"); o != "" {
		opts = append(opts, via.WithTrustedOrigin(o))
	}
	return opts
}

func main() {
	app := via.NewRouter(options()...)

	via.Mount(app, "/", content.Landing{})
	via.Mount(app, "/actions", content.Actions{})
	via.Mount(app, "/signals", content.Signals{})
	via.Mount(app, "/live", content.Live{})
	via.Mount(app, "/islands", content.Islands{})
	via.Mount(app, "/platform", content.Platform{})
	via.Mount(app, "/reference", content.Reference{})

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.Handle("/", app)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	srv := &http.Server{Addr: cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()

	// Close first: Shutdown does not cancel the router's own context, so an
	// open SSE response would hold it until its deadline.
	app.Close()
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shut); err != nil {
		log.Print(err)
	}
}
