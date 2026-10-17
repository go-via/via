package demo

import (
	"github.com/go-via/via/h"
	"go-via.dev/site/snippet"
)

// Source is the highlighted text of one demos file, unlabelled: the card's
// Source summary already names it. An unembedded name panics.
func Source(name string) h.H { return snippet.Show("demos/"+name, snippet.Title("")) }

// Plain is an unlabelled snippet.Text, for shell commands.
func Plain(src string) h.H { return snippet.Text("", src) }
