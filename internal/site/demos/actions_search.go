package demos

import (
	"strings"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

var stdlib = []string{
	"bufio", "bytes", "cmp", "context", "crypto/rand", "crypto/sha256", "database/sql",
	"embed", "encoding/base64", "encoding/csv", "encoding/json", "errors", "flag", "fmt",
	"hash/maphash", "html/template", "io", "io/fs", "iter", "log", "log/slog", "maps",
	"math", "math/big", "math/rand/v2", "mime/multipart", "net", "net/http",
	"net/http/httptest", "net/netip", "net/url", "os", "os/exec", "os/signal",
	"path/filepath", "reflect", "regexp", "runtime", "slices", "sort", "strconv",
	"strings", "sync", "sync/atomic", "testing", "text/template", "time", "unicode/utf8",
	"unique", "unsafe",
}

// Search filters on the server as you type. The input is a bound Signal, so
// every keystroke updates the signal in the browser; on.Debounce holds the
// POST until typing pauses, and the handler sees the latest value.
type Search struct {
	Query via.Signal[string]
	hits  []string
	asked bool
}

func (s *Search) Run(ctx *via.Ctx) {
	q := strings.ToLower(strings.TrimSpace(s.Query.Get()))
	s.asked, s.hits = q != "", nil
	for _, p := range stdlib {
		if q != "" && strings.Contains(p, q) {
			s.hits = append(s.hits, p)
		}
	}
}

func (s *Search) View() h.H {
	status := "Type part of a standard library package name."
	if s.asked && len(s.hits) == 0 {
		status = "No match."
	}
	rows := make([]h.H, 0, len(s.hits))
	for _, p := range s.hits {
		rows = append(rows, h.Li(h.Code(h.Str(p))))
	}
	return h.Div(
		h.Input(s.Query.Bind(), on.Input(s.Run, on.Debounce(300*time.Millisecond)),
			h.Type("search"), h.Placeholder("http, sync, …"), h.AutoComplete("off")),
		h.P(h.Class("notice"), h.Str(status)),
		h.Ul(rows...),
	)
}
