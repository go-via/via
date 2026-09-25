package h

import (
	"github.com/go-via/via/h"
)

// snippet:start escape
func Comment(who, text string) h.H {
	return h.P(h.Title(who), h.Str(text))
}

var Hostile = Comment(
	`"><b>`,
	"<script>alert(1)</script>",
)

// snippet:end

const CommentHTML = `<p title="&#34;&gt;&lt;b&gt;">
  &lt;script&gt;alert(1)&lt;/script&gt;
</p>`
