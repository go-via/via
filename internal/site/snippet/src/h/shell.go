package h

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// snippet:start shell
var Shell = via.WithHead(via.Head{
	Lang:      "en",
	HTMLAttrs: []via.Attr{{Name: "data-theme", Value: "dark"}},
	BodyAttrs: []via.Attr{{Name: "class", Value: "app"}},
})

type Settings struct{}

func (Settings) PageMeta() via.Meta {
	return via.Meta{
		Title:     "Settings",
		HTMLAttrs: []via.Attr{{Name: "data-theme", Value: "light"}},
		BodyAttrs: []via.Attr{{Name: "class", Value: "narrow"}},
	}
}

// snippet:end

func (Settings) View() h.H { return h.Div(h.Str("settings")) }

const ShellHTML = `<html lang="en" data-nonce="…" data-theme="light">
<body data-signals='{"viatab":""}' class="app narrow">`
