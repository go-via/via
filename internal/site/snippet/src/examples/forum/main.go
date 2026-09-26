// Command forum is a multi-page app - sign-up, sign-in, a profile with avatar
// upload, threads and posts - exercising the router features together:
// via.NewRouter + Mount, OnInit page data, PostForm + Redirect, ctx.Param[int],
// and an OnInit session check that redirects anonymous visitors.
//
// The store lives in store.go; it is plain app state, and via owns none of it.
package main

import (
	"cmp"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

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

// errorPage turns via's plain-text failures into documents. It runs for a
// mistyped URL as well as for a thread that no longer exists, so it may not
// assume any page resolved - ctx carries the request and the session, nothing
// more.
func errorPage(ctx *via.Ctx, e via.PageError) h.H {
	home := "/login"
	if _, ok := ctx.Session().Get[User](); ok {
		home = "/forum"
	}
	switch e.Reason {
	case via.ReasonNotFound:
		return page("No such page", h.P(h.Str("That thread or URL is gone.")), link(home, "Back to the forum"))
	case via.ReasonForbidden:
		return page("Not allowed", h.P(h.Str("Sign in again and retry.")), link("/login", "Log in"))
	default:
		return page("Something broke", h.P(h.Str(e.Detail)), link(home, "Back to the forum"))
	}
}

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

type Profile struct {
	store *Store
	user  User // loaded per request in OnInit
}

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

// SaveAvatar reads the uploaded file with stdlib and keeps it in the store -
// storage is entirely app-land. The profile points at /avatar/{id} because
// h.Src admits only http(s) and relative URLs; a data: URL renders as "#".
func (p *Profile) SaveAvatar(ctx *via.Ctx) {
	f, _, err := ctx.Request().FormFile("avatar")
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
	// The bytes are sniffed instead of trusting the client's Content-Type, and
	// DetectContentType never answers image/svg+xml, so no scriptable image is
	// stored.
	typ := http.DetectContentType(data)
	if !strings.HasPrefix(typ, "image/") {
		ctx.Redirect("/profile")
		return
	}
	p.store.setAvatar(p.user.ID, typ, data)
	p.user.Avatar = "/avatar/" + strconv.Itoa(p.user.ID)
	p.store.save(p.user)
	ctx.Session().Put(p.user)
	ctx.Redirect("/profile")
}

// serveAvatar answers GET /avatar/{user} with the stored upload.
func (s *Store) serveAvatar(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("user"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	typ, data, ok := s.avatar(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The URL stays the same across uploads.
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
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

type Forum struct {
	store   *Store
	threads []Thread
}

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

type ThreadPage struct {
	store   *Store
	id      int
	subject string
	posts   []Post
	found   bool
}

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

// snippet:start thread
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

// snippet:end

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
		via.WithErrorPage(errorPage),
	)

	via.Mount(app, "/signup", SignUp{store: store})
	via.Mount(app, "/login", Login{store: store})

	via.Mount(app, "/profile", Profile{store: store})
	via.Mount(app, "/forum", Forum{store: store})
	via.Mount(app, "/thread/{id}", ThreadPage{store: store})

	// "/" lands on the forum (its OnInit bounces an anonymous visitor to /login).
	http.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/forum", http.StatusSeeOther)
	})
	http.HandleFunc("GET /avatar/{user}", store.serveAvatar)
	http.Handle("/", app)

	log.Fatal(http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil))
}
