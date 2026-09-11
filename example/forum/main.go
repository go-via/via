// Command forum is a complete multi-page app — sign-up, sign-in, a profile with
// avatar upload, threads, and posts — that exercises every router feature at
// once: via.NewRouter + r.Mount (multi-page), OnInit (per-request page data),
// PostForm + Redirect (the server-rendered auth flow Datastar can't do, and the
// avatar upload — PostForm is always multipart, so a file input just needs
// ctx.Request().FormFile), ctx.Param[int] (the /thread/{id} segment), and an
// OnInit session check + Redirect (protected pages). It is the integration
// proof that the features compose. All app state is a plain in-memory store — the framework
// owns no storage. Zero '&', no closures at via call sites: lists render
// through method values.
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// --- app-land data + store (not framework) ---

// User is the session-stored identity. Plain app data.
type User struct {
	ID          int
	Email, Name string
	Avatar      string // a data: URL, set on upload
}

type Thread struct {
	ID            int
	Title, Author string
}

type Post struct{ Author, Body string }

// record holds a user plus their salted password hash (kept out of User so the
// hash never reaches the session or a render).
type record struct {
	user       User
	salt, hash []byte
}

// Store is the whole app's mutable state behind one mutex — users by email/id,
// threads, and posts per thread. Stdlib only; no database, no framework types.
type Store struct {
	mu      sync.Mutex
	byEmail map[string]*record
	byID    map[int]*record
	threads []Thread
	posts   map[int][]Post
	seqU    int
	seqT    int
}

func newStore() *Store {
	return &Store{byEmail: map[string]*record{}, byID: map[int]*record{}, posts: map[int][]Post{}}
}

// hashPass is a salted SHA-256 — a stdlib stand-in for bcrypt (which isn't a
// dependency here). Real apps should use a slow KDF; this keeps the example
// dependency-free while still never storing a plaintext password.
func hashPass(salt []byte, pass string) []byte {
	s := sha256.Sum256(append(append([]byte{}, salt...), pass...))
	return s[:]
}

func (s *Store) createUser(email, name, pass string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if email == "" || pass == "" {
		return User{}, errors.New("email and password are required")
	}
	if _, ok := s.byEmail[email]; ok {
		return User{}, errors.New("that email is already registered")
	}
	if name == "" {
		name = email
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	s.seqU++
	u := User{ID: s.seqU, Email: email, Name: name}
	rec := &record{user: u, salt: salt, hash: hashPass(salt, pass)}
	s.byEmail[email] = rec
	s.byID[u.ID] = rec
	return u, nil
}

func (s *Store) authenticate(email, pass string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byEmail[email]
	if !ok || !hmac.Equal(rec.hash, hashPass(rec.salt, pass)) {
		return User{}, false
	}
	return rec.user, true
}

func (s *Store) save(u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.byID[u.ID]; ok {
		rec.user = u
	}
}

func (s *Store) allThreads() []Thread {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Thread{}, s.threads...)
}

func (s *Store) newThread(author, title string) {
	if title == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seqT++
	s.threads = append(s.threads, Thread{ID: s.seqT, Title: title, Author: author})
}

func (s *Store) thread(id int) (string, []Post) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var title string
	for _, t := range s.threads {
		if t.ID == id {
			title = t.Title
		}
	}
	return title, append([]Post{}, s.posts[id]...)
}

func (s *Store) reply(id int, author, body string) {
	if body == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.posts[id] = append(s.posts[id], Post{Author: author, Body: body})
}

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

// --- /signup ---

type SignUp struct {
	store *Store
	err   string
}

func (s *SignUp) Submit(ctx *via.Ctx) {
	r := ctx.Request()
	u, err := s.store.createUser(r.FormValue("email"), r.FormValue("name"), r.FormValue("password"))
	if err != nil {
		s.err = err.Error() // no Redirect → the page re-renders with the error
		return
	}
	ctx.Session().Put(u)
	ctx.Session().Rotate() // fixation defense: new session id on privilege change
	ctx.Redirect("/forum")
}

func (s *SignUp) errNote() h.H { return h.P(h.Str(s.err)) }

func (s *SignUp) View() h.H {
	return page("Sign up",
		via.PostForm(s.Submit,
			field("email", "email", "email"),
			field("name", "text", "display name"),
			field("password", "password", "password"),
			via.When(s.err != "", s.errNote),
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
	r := ctx.Request()
	u, ok := l.store.authenticate(r.FormValue("email"), r.FormValue("password"))
	if !ok {
		l.err = "wrong email or password"
		return
	}
	ctx.Session().Put(u)
	ctx.Session().Rotate()
	ctx.Redirect("/forum")
}

func (l *Login) errNote() h.H { return h.P(h.Str(l.err)) }

func (l *Login) View() h.H {
	return page("Log in",
		via.PostForm(l.Submit,
			field("email", "email", "email"),
			field("password", "password", "password"),
			via.When(l.err != "", l.errNote),
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
// data: URL — storage is entirely app-land.
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

func (p *Profile) View() h.H {
	return page("Profile — "+p.user.Name,
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

func (f *Forum) OnInit(ctx *via.Ctx) error {
	if _, ok := ctx.Session().Get[User](); !ok {
		ctx.Redirect("/login")
		return nil
	}
	f.threads = f.store.allThreads()
	return nil
}

func (f *Forum) New(ctx *via.Ctx) {
	u, _ := ctx.Session().Get[User]()
	f.store.newThread(u.Name, ctx.Request().FormValue("title"))
	ctx.Redirect("/forum")
}

func (f *Forum) row(t Thread) h.H {
	return h.Li(link("/thread/"+strconv.Itoa(t.ID), t.Title), h.Str(" — "+t.Author))
}

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
	store *Store
	id    int
	title string
	posts []Post
}

func (p *ThreadPage) OnInit(ctx *via.Ctx) error {
	if _, ok := ctx.Session().Get[User](); !ok {
		ctx.Redirect("/login")
		return nil
	}
	p.id = ctx.Param[int]("id")
	p.title, p.posts = p.store.thread(p.id)
	return nil
}

func (p *ThreadPage) Send(ctx *via.Ctx) {
	u, _ := ctx.Session().Get[User]()
	p.store.reply(p.id, u.Name, ctx.Request().FormValue("body"))
	ctx.Redirect("/thread/" + strconv.Itoa(p.id))
}

func (p *ThreadPage) postRow(po Post) h.H {
	return h.Li(h.B(h.Str(po.Author+": ")), h.Str(po.Body))
}

func (p *ThreadPage) View() h.H {
	return page(p.title,
		h.Ul(via.Each(p.posts, p.postRow)),
		via.PostForm(p.Send,
			valField("body", "text", ""),
			h.Button(h.Str("Reply")),
		),
		link("/forum", "Back to the forum"),
	)
}

func main() {
	store := newStore()
	// A fixed demo key keeps sessions valid across restarts; generate a random
	// one per deploy in a real app (omit WithSessionKey to auto-generate).
	app := via.NewRouter(via.WithSessionKey([]byte("forum-demo-session-signing-key!!")))

	app.Mount("/signup", SignUp{store: store})
	app.Mount("/login", Login{store: store})

	app.Mount("/profile", Profile{store: store})
	app.Mount("/forum", Forum{store: store})
	app.Mount("/thread/{id}", ThreadPage{store: store})

	// "/" lands on the forum (its OnInit bounces an anonymous visitor to /login).
	root := http.NewServeMux()
	root.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/forum", http.StatusSeeOther)
	})
	root.Handle("/", app)

	log.Println("forum on http://localhost:8941")
	log.Fatal(http.ListenAndServe(":8941", root))
}
