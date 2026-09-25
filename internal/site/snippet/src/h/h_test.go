package h_test

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/demos"
	hs "go-via.dev/site/snippet/src/h"
)

var (
	afterTag  = regexp.MustCompile(`>\s*\n\s*`)
	beforeTag = regexp.MustCompile(`\s*\n\s*<`)
	inTag     = regexp.MustCompile(`\s*\n\s*`)
	root      = regexp.MustCompile(`(?s)<div id="root" data-signals='\{\}'>(.*)</div></body>`)
)

// flat undoes the line breaks the page shows: one beside a tag boundary
// vanishes, and any other is a single space.
func flat(s string) string {
	s = afterTag.ReplaceAllString(strings.TrimSpace(s), ">")
	return inTag.ReplaceAllString(beforeTag.ReplaceAllString(s, "<"), " ")
}

type node struct{ n h.H }

func (n *node) View() h.H { return n.n }

type colonPage struct{ Colon demos.HColon }

func (p *colonPage) View() h.H { return via.Child(p.Colon) }

func body(t *testing.T, r *via.Router) string {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, 200, rec.Code)
	return rec.Body.String()
}

func TestShownHTML_matchesTheRender(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		node  h.H
		shown string
	}{
		{"card", hs.Inbox, hs.CardHTML},
		{"qty", hs.InStock, hs.QtyHTML},
		{"comment", hs.Hostile, hs.CommentHTML},
		{"links", hs.Links(), hs.LinksHTML},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := root.FindStringSubmatch(body(t, via.Handler(node{tt.node})))
			require.Len(t, m, 2)
			assert.Equal(t, flat(tt.shown), m[1])
		})
	}
}

func TestInviteHTML_matchesTheRender(t *testing.T) {
	t.Parallel()
	got := body(t, via.Handler(hs.Invite{URL: "https://example.com/join?ref=@ada"}))
	assert.Contains(t, got, flat(hs.InviteHTML))
}

func TestColonHTML_matchesTheDemo(t *testing.T) {
	t.Parallel()
	assert.Contains(t, body(t, via.Handler(colonPage{})), flat(hs.ColonHTML))
}
