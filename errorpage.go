package via

import (
	"bytes"
	"errors"
	"html"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// Reason is the stable code a [WithErrorPage] handler switches on, so an app
// never has to parse via's plain-text bodies. One reason per status class:
// the STATUS is the contract, the body text is not.
type Reason string

// The reasons via reports. A status via does not currently emit maps to
// ReasonInternal (5xx) or ReasonBadRequest (4xx), so a switch with a default
// is always exhaustive.
const (
	ReasonBadRequest       Reason = "bad_request"        // 400 — unusable action arg, malformed form or body
	ReasonForbidden        Reason = "forbidden"          // 403 — untrusted origin, session mismatch, Guard denial
	ReasonNotFound         Reason = "not_found"          // 404 — no such route, ErrNotFound, undecodable Param
	ReasonMethodNotAllowed Reason = "method_not_allowed" // 405 — the route exists, this method does not (a GET of an action URL)
	ReasonGone             Reason = "gone"               // 410 — the render that would bind this action is gone
	ReasonTooLarge         Reason = "too_large"          // 413 — body over the cap
	ReasonInternal         Reason = "internal"           // 500 — a hook or render failed
	ReasonUnavailable      Reason = "unavailable"        // 503 — the session store could not answer
)

// The errors via reports through [PageError].Err for the failures an app can
// reasonably answer differently from the rest of their status class. Match
// with errors.Is; Err is nil for every other failure, so a switch must have a
// default.
//
// There is deliberately no sentinel for the 503s the SSE connect raises (at
// capacity, shutting down): that route is client-consumed and never renders an
// error page, so a sentinel for it would be unreachable API. Nor for an unknown
// action or a cross-mount child id — both are programming mistakes with the
// same answer, "something went wrong", not a distinct page.
var (
	// ErrStoreDown means the session store could not be read for this request.
	// The request itself was fine; a dependency is not. Answer it like an
	// outage — a status page, not a retry prompt.
	ErrStoreDown = errors.New("via: session store unavailable")

	// ErrStaleTab means the tab that would have bound this action is gone: its
	// stream closed, or the id belongs to a render that no longer exists. The
	// one failure a reload actually fixes.
	ErrStaleTab = errors.New("via: stale tab")

	// ErrForbidden denies with a 403. Return it from a Guard or an OnInit for
	// "you may not do this"; queue a Ctx.Redirect instead for "please sign in".
	ErrForbidden = errors.New("via: forbidden")
)

// PageError is everything via knows about a failure it is about to answer.
// Status and Reason are the contract; Detail and Err are for logging and for
// dev builds, and either may be empty.
type PageError struct {
	// Status is the HTTP status via will send. An error page never changes it.
	Status int

	// Reason is the stable code to switch on.
	Reason Reason

	// Detail is the plain-text body via would have sent on its own. Safe to
	// show — it names no user data — but it is prose, not an API.
	Detail string

	// Err is the underlying error when one exists (a failed OnInit/OnReload,
	// a recovered panic), nil otherwise.
	Err error
}

// WithErrorPage renders via's failures as HTML documents instead of plain text.
//
// DOCUMENT RESPONSES ONLY. It applies to a response the browser will render as
// a page: a GET of a mounted page, a route that matches no mount, and a native
// <form> submit (which navigates). It deliberately does NOT apply to a Datastar
// @post or to the SSE connect — those bodies are consumed by the client, which
// already surfaces a failure itself, so an HTML document there is dead weight
// that would only be logged to a console. Those keep their plain-text bodies.
//
// The handler gets a Ctx carrying the request and the session — enough for a
// "you are signed in as …" header — but no composition: it runs for failures
// that happen before any page resolves, so there is nothing to embed and
// ctx.Redirect, ctx.Tick and ctx.Listen are ignored.
//
// STYLING AN ACTION FAILURE is a client-side job, deliberately. A @post that
// answers 400/403/410/503 surfaces its status through Datastar, which fires a
// datastar-fetch error event on the element that made the request — listen for
// that and render the banner you want (see reconnect.go for the shape via's own
// reconnect notice uses). There is no server-rendered equivalent, because an
// action's response patches elements rather than replacing the document.
//
// COST: a failed request that reaches the handler resolves the session a SECOND
// time — once on the way in, once for the Ctx the handler gets — so an error
// page is one extra store read per failure. It is off the happy path, but a
// 404-flooded endpoint pays it per request.
//
// [Head].Raw is emitted VERBATIM into the error document's <head>, the same as
// on a normal page. Anything unsafe there is unsafe here too, on a response the
// app did not choose to serve.
//
// It may not make things worse. Returning nil, or panicking, falls back to the
// exact plain-text response via would have sent and logs once.
//
// CSP: an error page may render before a mount resolves, or for a different
// mount than the one in hand, so it gets the ROUTER-WIDE floor — the policy
// built from [Head].Assets alone. It cannot widen a mount's policy and it
// carries no per-page assets; declare anything it needs in the router's Head.
//
//	via.NewRouter(via.WithErrorPage(func(ctx *via.Ctx, e via.PageError) h.H {
//		if e.Reason == via.ReasonNotFound {
//			return h.Div(h.H1(h.Str("No such page")), h.A(h.Href("/"), h.Str("Home")))
//		}
//		return h.Div(h.H1(h.Str("Something broke")))
//	}))
func WithErrorPage(fn func(*Ctx, PageError) h.H) Option {
	return func(c *config) {
		if fn == nil {
			panic("via: WithErrorPage(nil)")
		}
		if c.errorPage != nil {
			panic("via: conflicting WithErrorPage options")
		}
		c.errorPage = fn
	}
}

func reasonFor(status int) Reason {
	switch status {
	case http.StatusBadRequest:
		return ReasonBadRequest
	case http.StatusForbidden:
		return ReasonForbidden
	case http.StatusNotFound:
		return ReasonNotFound
	case http.StatusMethodNotAllowed:
		return ReasonMethodNotAllowed
	case http.StatusGone:
		return ReasonGone
	case http.StatusRequestEntityTooLarge:
		return ReasonTooLarge
	case http.StatusServiceUnavailable:
		return ReasonUnavailable
	case http.StatusInternalServerError:
		return ReasonInternal
	}
	if status >= 500 {
		return ReasonInternal
	}
	return ReasonBadRequest
}

// errPageBodyCap bounds what a failed response's plain body can buffer. Every
// one via writes is a short constant; the cap exists only so a future one that
// isn't cannot be held whole in memory.
const errPageBodyCap = 4 << 10

// errPageWriter defers a >=400 response until the handler has returned, so the
// error page is rendered from ONE place with the router-wide CSP rather than at
// each of via's ~40 http.Error sites. Intercepting the write instead of
// rewriting those sites is what guarantees the status and the plain-text
// fallback body stay exactly what they were.
type errPageWriter struct {
	http.ResponseWriter
	r      *Router
	req    *http.Request
	status int
	body   bytes.Buffer
	caught bool
	passed bool
	err    error
}

// Unwrap lets http.NewResponseController reach the real writer.
func (e *errPageWriter) Unwrap() http.ResponseWriter { return e.ResponseWriter }

// commit reports whether this write may reach the real writer, marking the
// response committed when it may. A caught response is buffered until finish
// instead, so nothing downstream may see it yet.
func (e *errPageWriter) commit() bool {
	if e.caught {
		return false
	}
	e.passed = true
	return true
}

// unwrapWriter reaches the writer net/http itself installed, past any wrapper.
// http.MaxBytesReader needs it: it type-asserts an unexported interface on the
// writer to close the connection the moment a body overruns, and a wrapper
// hides that — costing a 256KB drain of the oversize body on every 413.
func unwrapWriter(w http.ResponseWriter) http.ResponseWriter {
	for {
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return w
		}
		w = u.Unwrap()
	}
}

func (e *errPageWriter) WriteHeader(code int) {
	if e.passed {
		return
	}
	if !e.caught && code >= 400 {
		e.caught, e.status = true, code
		return
	}
	if !e.commit() {
		return
	}
	e.ResponseWriter.WriteHeader(code)
}

func (e *errPageWriter) Write(p []byte) (int, error) {
	if !e.commit() {
		if n := errPageBodyCap - e.body.Len(); n > 0 {
			e.body.Write(p[:min(n, len(p))])
		}
		return len(p), nil
	}
	return e.ResponseWriter.Write(p)
}

func (e *errPageWriter) finish() {
	if !e.caught {
		return
	}
	e.r.writeErrorPage(e.ResponseWriter, e.req, PageError{
		Status: e.status,
		Reason: reasonFor(e.status),
		Detail: strings.TrimRight(e.body.String(), "\n"),
		Err:    e.err,
	})
}

// noteErr hands the underlying error to the error page, when one is being
// built. A no-op otherwise, which is why via's failure sites can call it
// without caring whether WithErrorPage is set.
func noteErr(w http.ResponseWriter, err error) {
	if e, ok := w.(*errPageWriter); ok && e.err == nil {
		e.err = err
	}
}

// wrapForErrorPage decides whether this request's failures are DOCUMENT
// responses. A Datastar @post and the SSE connect are client-consumed, so they
// stay plain text (see WithErrorPage).
//
// A new STREAMING route has to be excluded here too: the wrapper buffers a
// >=400 response until finish, and a flush that reaches the real writer past it
// would commit a bare 200 ahead of the error page.
func (r *Router) wrapForErrorPage(w http.ResponseWriter, req *http.Request) *errPageWriter {
	if r.cfg.errorPage == nil ||
		req.Header.Get("Datastar-Request") == "true" ||
		strings.HasSuffix(req.URL.Path, "/_via/sse") {
		return nil
	}
	return &errPageWriter{ResponseWriter: w, r: r, req: req}
}

func (r *Router) writeErrorPage(w http.ResponseWriter, req *http.Request, pe PageError) {
	body, ok := r.renderErrorPage(req, pe)
	hdr := w.Header()
	hdr.Del("Content-Length")
	hdr.Set("X-Content-Type-Options", "nosniff")
	if !ok {
		hdr.Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(pe.Status)
		w.Write([]byte(pe.Detail + "\n"))
		return
	}
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	hdr.Set("Content-Security-Policy", r.errCSP)
	var head strings.Builder
	head.WriteString(`<!doctype html>` + r.cfg.head.htmlOpen() + `<head><meta charset="utf-8">`)
	head.WriteString(`<title>` + html.EscapeString(strconv.Itoa(pe.Status)+" "+http.StatusText(pe.Status)) + `</title>`)
	head.WriteString(r.cfg.head.Raw)
	r.cfg.head.Assets.render(&head)
	head.WriteString(`</head><body>`)
	w.WriteHeader(pe.Status)
	w.Write([]byte(head.String()))
	w.Write(body)
	w.Write([]byte(`</body></html>`))
}

// renderErrorPage runs the app's handler behind a recover: an error page that
// panics while answering a 500 is the worst outcome this feature could have, so
// it degrades to the plain text via would have sent and says so once.
func (r *Router) renderErrorPage(req *http.Request, pe PageError) (body []byte, ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			r.errPageWarn.Do(func() {
				log.Printf("via: the WithErrorPage handler panicked (%v) — falling back to via's plain-text errors", rec)
			})
			body, ok = nil, false
		}
	}()
	ctx := newRootCtx(false, "", nil)
	ctx.errPage = true
	ctx.req = req
	ctx.sessions = r.sessions
	node := r.cfg.errorPage(ctx, pe)
	if node == nil {
		r.errPageWarn.Do(func() {
			log.Printf("via: the WithErrorPage handler returned nil for %d %s — falling back to via's plain-text errors", pe.Status, pe.Reason)
		})
		return nil, false
	}
	rr := hcore.NewRenderer(binderCtx{ctx})
	rr.Render(node)
	return rr.Bytes(), true
}
