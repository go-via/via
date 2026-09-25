package h

import (
	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
)

// snippet:start expr
type Invite struct {
	Query via.Signal[string]
	URL   string
}

func (p *Invite) View() h.H {
	q := p.Query.Ref()
	copyURL := expr.CopyToClipboard(expr.Val(p.URL))
	return h.Div(
		h.Input(p.Query.Bind()),
		h.P(h.DataShow(q.Ne("")),
			h.Str("Searching for "), p.Query.Display()),
		h.Button(h.DataOn("click", copyURL), h.Str("Copy invite link")),
		h.Pre(h.Code(h.Str("go get github.com/go-via/via"))),
		h.Button(h.DataOn("click", expr.CopyTextOf("code"),
			expr.Class("copied", true)), h.Str("Copy")),
	)
}

// snippet:end

// InviteHTML is Invite's render with URL "https://example.com/join?ref=@ada".
const InviteHTML = `<div id="root" data-signals='{"query":""}'>
  <div>
    <input data-bind="query">
    <p data-show="($query !== &#34;&#34;)">
      Searching for <span data-text="$query"></span>
    </p>
    <button data-on:click="navigator.clipboard.writeText(&#34;https://example.com/join?ref=\u0040ada&#34;)">Copy invite link</button>
    <pre><code>go get github.com/go-via/via</code></pre>
    <button data-on:click="navigator.clipboard.writeText(el.parentElement?.querySelector(&#34;code&#34;)?.textContent ?? &#34;&#34;);
      el.classList.toggle(&#34;copied&#34;, true)">Copy</button>
  </div>
</div>`
