// Command forum is a multi-page app - sign-up, sign-in, a profile with avatar
// upload, threads and posts - exercising the router features together:
// via.NewRouter + Mount, OnInit page data, PostForm + Redirect, ctx.Param[int],
// and an OnInit session check that redirects anonymous visitors.
//
// The store lives in store.go; it is plain app state, and via owns none of it.
package main

import (
	"cmp"
	"encoding/base64"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// --- small HTML helpers (plain app glue, not framework API) ---

func page(title string, body ...h.H) h.H {
	return h.Div(append([]h.H{h.H1(h.Str(title))}, body...)...)
}

func field(name, typ, placeholder string) h.H {
	return h.Div(h.Input(h.Type(typ), h.Name(name), h.Placeholder(placeholder)))
}

func valField(name, typ, val string) h.H {
	return h.Input(h.Type(typ), h.Name(name), h.Value(val))
}

func link(href, text string) h.H { return h.A(h.Href(href), h.Str(text)) }

func note(msg string) func() h.H { return func() h.H { return h.P(h.Str(msg)) } }

// --- /signup ---

type SignUp struct {
	store *Store
	err   string
}

func (s *SignUp) Submit(ctx *via.Ctx) {
	r := ctx.Request()
	u, err := s.store.createUser(r.FormValue("email"), r.FormValue("name"))
	if err != nil {
		s.err = err.Error() // no Redirect -> the page re-renders with the error
		return
	}
	ctx.Session().Put(u)
	ctx.Session().Rotate() // fixation defense: new session id on privilege change
	ctx.Redirect("/forum")
}

func (s *SignUp) PageMeta() via.Meta { return via.Meta{Title: "Sign up — Forum"} }

func (s *SignUp) View() h.H {
	return page("Sign up",
		via.PostForm(s.Submit,
			field("email", "email", "email"),
			field("name", "text", "display name"),
			via.When(s.err != "", note(s.err)),
			h.Button(h.Str("Create account")),
		),
		link("/login", "Already have an account? Log in"),
	)
}

// --- /login ---

type Login struct {
	store *Store
	err   string
}

func (l *Login) Submit(ctx *via.Ctx) {
	u, ok := l.store.lookup(ctx.Request().FormValue("email"))
	if !ok {
		l.err = "no account with that email"
		return
	}
	ctx.Session().Put(u)
	ctx.Session().Rotate()
	ctx.Redirect("/forum")
}

func (l *Login) PageMeta() via.Meta { return via.Meta{Title: "Sign in — Forum"} }

func (l *Login) View() h.H {
	return page("Log in",
		via.PostForm(l.Submit,
			field("email", "email", "email"),
			via.When(l.err != "", note(l.err)),
			h.Button(h.Str("Log in")),
		),
		link("/signup", "Need an account? Sign up"),
	)
}

// --- /profile (session-gated in OnInit) ---

type Profile struct {
	store *Store
	user  User // loaded per request in OnInit
}

var _ via.Initer = (*Profile)(nil)

func (p *Profile) OnInit(ctx *via.Ctx) error {
	user, ok := ctx.Session().Get[User]()
	if !ok {
		ctx.Redirect("/login")
		return nil
	}
	p.user = user
	return nil
}

func (p *Profile) SaveName(ctx *via.Ctx) {
	p.user.Name = ctx.Request().FormValue("name")
	p.store.save(p.user)
	ctx.Session().Put(p.user)
	ctx.Redirect("/profile")
}

// SaveAvatar reads the uploaded file with stdlib and stores it inline as a
// data: URL - storage is entirely app-land.
func (p *Profile) SaveAvatar(ctx *via.Ctx) {
	f, hdr, err := ctx.Request().FormFile("avatar")
	if err != nil {
		ctx.Redirect("/profile")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil || len(data) == 0 {
		ctx.Redirect("/profile")
		return
	}
	// Demo simplification: the client-declared Content-Type is trusted as-is. A
	// data: URL in an <img src> is not script-executable, but a real app should
	// sniff the bytes and constrain the type before storing/serving it.
	p.user.Avatar = "data:" + hdr.Header.Get("Content-Type") + ";base64," + base64.StdEncoding.EncodeToString(data)
	p.store.save(p.user)
	ctx.Session().Put(p.user)
	ctx.Redirect("/profile")
}

func (p *Profile) avatarImg() h.H { return h.Img(h.Src(p.user.Avatar), h.Width(96)) }

func (p *Profile) PageMeta() via.Meta { return via.Meta{Title: "Your profile — Forum"} }

func (p *Profile) View() h.H {
	return page("Profile - "+p.user.Name,
		via.When(p.user.Avatar != "", p.avatarImg),
		via.PostForm(p.SaveAvatar,
			h.Input(h.Type("file"), h.Name("avatar")),
			h.Button(h.Str("Upload avatar")),
		),
		via.PostForm(p.SaveName,
			valField("name", "text", p.user.Name),
			h.Button(h.Str("Save name")),
		),
		link("/forum", "Back to the forum"),
	)
}

// --- /forum (session-gated in OnInit) ---

type Forum struct {
	store   *Store
	threads []Thread
}

var _ via.Initer = (*Forum)(nil)
var _ via.Reloader = (*Forum)(nil)

func (f *Forum) OnInit(ctx *via.Ctx) error {
	if _, ok := ctx.Session().Get[User](); !ok {
		ctx.Redirect("/login")
		return nil
	}
	return f.OnReload(ctx)
}

// Reload re-reads the thread list after one of this page's actions ran. Without
// it New would write a thread the response render never sees, because OnInit
// filled f.threads before the handler touched the store.
func (f *Forum) OnReload(ctx *via.Ctx) error { f.threads = f.store.allThreads(); return nil }

func (f *Forum) New(ctx *via.Ctx) {
	u, _ := ctx.Session().Get[User]()
	f.store.newThread(u.Name, ctx.Request().FormValue("title"))
	ctx.Redirect("/forum")
}

func (f *Forum) row(t Thread) h.H {
	return h.Li(link("/thread/"+strconv.Itoa(t.ID), t.Title), h.Str(" - "+t.Author))
}

func (f *Forum) PageMeta() via.Meta { return via.Meta{Title: "Threads — Forum"} }

func (f *Forum) View() h.H {
	return page("Forum",
		h.Ul(via.Each(f.threads, f.row)),
		via.PostForm(f.New,
			valField("title", "text", ""),
			h.Button(h.Str("New thread")),
		),
		link("/profile", "Profile"),
	)
}

// --- /thread/{id} (session-gated in OnInit; reads the {id} segment via Param) ---

type ThreadPage struct {
	store   *Store
	id      int
	subject string
	posts   []Post
	found   bool
}

var _ via.Initer = (*ThreadPage)(nil)
var _ via.Reloader = (*ThreadPage)(nil)
var _ via.PageMetaer = (*ThreadPage)(nil)

// PageMeta runs after OnReload, so the tab strip names the thread that was
// just loaded — the case a Head field could not serve. Assets is absent here
// because it may not depend on the loaded thread; only the inert slots may.
func (p *ThreadPage) PageMeta() via.Meta {
	if !p.found {
		return via.Meta{Title: "No such thread — Forum", Robots: "noindex"}
	}
	return via.Meta{
		Title:       p.subject + " — Forum",
		Description: "Discussion: " + p.subject,
		OG:          map[string]string{"title": p.subject, "type": "article"},
	}
}

func (p *ThreadPage) OnInit(ctx *via.Ctx) error {
	if _, ok := ctx.Session().Get[User](); !ok {
		ctx.Redirect("/login")
		return nil
	}
	p.id = ctx.Param[int]("id")
	return p.OnReload(ctx)
}

func (p *ThreadPage) OnReload(ctx *via.Ctx) error {
	p.subject, p.posts, p.found = p.store.thread(p.id)
	return nil
}

// Send needs no Redirect: Reload re-reads the thread, so the reply is in the
// response render.
func (p *ThreadPage) Send(ctx *via.Ctx) {
	if !p.found {
		return
	}
	u, _ := ctx.Session().Get[User]()
	p.store.reply(p.id, u.Name, ctx.Request().FormValue("body"))
}

func (p *ThreadPage) postRow(po Post) h.H {
	return h.Li(h.B(h.Str(po.Author+": ")), h.Str(po.Body))
}

func (p *ThreadPage) replyForm() h.H {
	return via.PostForm(p.Send,
		valField("body", "text", ""),
		h.Button(h.Str("Reply")),
	)
}

func (p *ThreadPage) View() h.H {
	if !p.found {
		return page("No such thread", link("/forum", "Back to the forum"))
	}
	return page(p.subject,
		h.Ul(via.Each(p.posts, p.postRow)),
		p.replyForm(),
		link("/forum", "Back to the forum"),
	)
}

func main() {
	// The cookie signing key must outlive the process, or every restart logs
	// everyone out. Never hardcode one.
	key := os.Getenv("VIA_SESSION_KEY")
	if key == "" {
		log.Fatal("VIA_SESSION_KEY is unset: export 32+ random bytes before starting the forum")
	}

	store := newStore()
	// Head is router-wide; each page names itself with PageMeta().
	app := via.NewRouter(
		via.WithSessionKey([]byte(key)),
		via.WithHead(via.Head{Lang: "en"}),
	)

	app.Mount("/signup", SignUp{store: store})
	app.Mount("/login", Login{store: store})

	app.Mount("/profile", Profile{store: store})
	app.Mount("/forum", Forum{store: store})
	app.Mount("/thread/{id}", ThreadPage{store: store})

	// "/" lands on the forum (its OnInit bounces an anonymous visitor to /login).
	http.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/forum", http.StatusSeeOther)
	})
	http.Handle("/", app)

	log.Fatal(http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil))
}
