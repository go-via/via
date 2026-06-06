// Package assets embeds Signal brand assets (svg/png/css) served under /assets/.
package assets

import (
	"embed"

	"github.com/go-via/via/h"
)

//go:embed static
var FS embed.FS

// themeRestore is a classic (non-module) inline script. It runs during head
// parse — BEFORE datastar's deferred module reads <meta data-signals> — and
// overrides the picocss theme/mode signals from localStorage. That makes a
// chosen theme persist across page navigations with no flash and no server
// round-trip, and keeps the theme picker in sync (it binds the same signals).
const themeRestore = `(function(){try{` +
	`var m=document.querySelector('meta[data-signals]');if(!m)return;` +
	`var s=JSON.parse(m.getAttribute('data-signals'));` +
	`var t=localStorage.getItem('signal-theme'),d=localStorage.getItem('signal-mode');` +
	`if(t)s._picoTheme=t;if(d)s._picoDarkMode=d;` +
	`m.setAttribute('data-signals',JSON.stringify(s));` +
	`}catch(e){}})();`

// Head returns the <head> nodes wiring the favicon, custom.css, and the
// theme-restore script. Paths assume HandleStatic is mounted at "/assets/".
func Head() []h.H {
	return []h.H{
		h.Link(h.Rel("icon"), h.Type("image/svg+xml"), h.Href("/assets/static/icon.svg")),
		h.Link(h.Rel("stylesheet"), h.Href("/assets/static/custom.css")),
		h.Script(h.Raw(themeRestore)),
	}
}

// Wordmark / Icon are the public asset URLs for use in compositions.
const (
	Wordmark = "/assets/static/wordmark.svg"
	Icon     = "/assets/static/icon.svg"
	Punch    = "/assets/static/punch-dark.png"
)
