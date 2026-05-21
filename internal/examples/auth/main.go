// Auth demonstrates the typed-session helpers and middleware-driven
// authentication. One in-memory user store; three pages: login,
// register, profile (protected).
//
//	go run ./internal/examples/auth
//	open http://localhost:3000
package main

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/plugins/picocss"
	"github.com/go-via/via/sess"
	"golang.org/x/crypto/bcrypt"
)

// User is the typed value we hang off the session.
type User struct {
	Email string
	Name  string
}

// Store is the in-memory user database.
type Store struct {
	mu     sync.RWMutex
	users  map[string]User
	hashes map[string][]byte
}

func NewStore() *Store {
	return &Store{users: map[string]User{}, hashes: map[string][]byte{}}
}

var (
	errAllFieldsRequired  = errors.New("all fields are required")
	errEmailAlreadyExists = errors.New("email already registered")
	errInvalidCredentials = errors.New("invalid email or password")
)

func (s *Store) Register(name, email, password string) error {
	if name == "" || email == "" || password == "" {
		return errAllFieldsRequired
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.hashes[email]; exists {
		return errEmailAlreadyExists
	}
	s.users[email] = User{Name: name, Email: email}
	s.hashes[email] = hash
	return nil
}

func (s *Store) Authenticate(email, password string) (User, error) {
	s.mu.RLock()
	hash, ok := s.hashes[email]
	user := s.users[email]
	s.mu.RUnlock()
	if !ok {
		return User{}, errInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		return User{}, errInvalidCredentials
	}
	return user, nil
}

var store = NewStore()

// Pages

type LoginPage struct {
	Email    via.SignalStr
	Password via.SignalStr
	Err      via.StateTabStr
}

func (p *LoginPage) Submit(ctx *via.Ctx) error {
	user, err := store.Authenticate(strings.TrimSpace(p.Email.Read(ctx)), p.Password.Read(ctx))
	if err != nil {
		p.Err.Write(ctx, err.Error())
		return nil
	}
	sess.Rotate(ctx)
	sess.Put(ctx, user)
	return via.Redirect("/profile")
}

func (p *LoginPage) View(ctx *via.CtxR) h.H {
	return shell(ctx, h.Div(
		h.H1(h.Text("Login")),
		errBanner(p.Err.Read(ctx)),
		h.Label(h.Text("Email"),
			h.Input(h.Type("email"), p.Email.Bind(), h.Placeholder("you@example.com")),
		),
		h.Label(h.Text("Password"),
			h.Input(h.Type("password"), p.Password.Bind(), h.Placeholder("Password")),
		),
		h.Button(h.Text("Log in"), on.Click(p.Submit)),
		h.P(h.Small(h.Text("Don't have an account? "),
			h.A(h.Href("/register"), h.Text("Register")))),
	))
}

type RegisterPage struct {
	Name     via.SignalStr
	Email    via.SignalStr
	Password via.SignalStr
	Err      via.StateTabStr
}

func (p *RegisterPage) Submit(ctx *via.Ctx) error {
	if err := store.Register(
		strings.TrimSpace(p.Name.Read(ctx)),
		strings.TrimSpace(p.Email.Read(ctx)),
		p.Password.Read(ctx),
	); err != nil {
		p.Err.Write(ctx, err.Error())
		return nil
	}
	return via.Redirect("/login")
}

func (p *RegisterPage) View(ctx *via.CtxR) h.H {
	return shell(ctx, h.Div(
		h.H1(h.Text("Register")),
		errBanner(p.Err.Read(ctx)),
		h.Label(h.Text("Name"), h.Input(h.Type("text"), p.Name.Bind())),
		h.Label(h.Text("Email"), h.Input(h.Type("email"), p.Email.Bind())),
		h.Label(h.Text("Password"), h.Input(h.Type("password"), p.Password.Bind())),
		h.Button(h.Text("Create account"), on.Click(p.Submit)),
	))
}

func errBanner(msg string) h.H {
	if msg == "" {
		return nil
	}
	return h.Article(
		h.Style("border-left:3px solid var(--pico-del-color);color:var(--pico-del-color)"),
		h.Small(h.Text(msg)),
	)
}

type ProfilePage struct{}

func (p *ProfilePage) Logout(ctx *via.Ctx) error {
	sess.Clear[User](ctx)
	sess.Rotate(ctx)
	return via.Redirect("/")
}

func (p *ProfilePage) View(ctx *via.CtxR) h.H {
	user, _ := sess.Get[User](ctx)
	return shell(ctx, h.Div(
		h.H1(h.Textf("Hello, %s", user.Name)),
		h.P(h.Text("Signed in as "), h.Code(h.Text(user.Email))),
		h.Button(h.Class("outline secondary"), h.Text("Log out"), on.Click(p.Logout)),
	))
}

type LandingPage struct{}

func (p *LandingPage) View(ctx *via.CtxR) h.H {
	return shell(ctx, h.Div(
		h.H1(h.Text("Via Auth Demo")),
		h.P(h.Text("Typed sessions, sess.Rotate on login, StateSess-style data with auto-render.")),
	))
}

// shell wraps every page in the same nav so the auth state shows up in
// the header. We don't have a Layout primitive yet — composing with a
// helper function is just as clean for a flat app.
func shell(ctx *via.CtxR, content h.H) h.H {
	_, loggedIn := sess.Get[User](ctx)
	return h.Div(
		h.Nav(h.Class("container"),
			h.Ul(h.Li(h.A(h.Href("/"), h.Strong(h.Text("⚡ Via Auth"))))),
			h.Ul(
				h.IfElse(loggedIn,
					h.Li(h.A(h.Href("/profile"), h.Text("Profile"))),
					h.Li(h.A(h.Href("/login"), h.Text("Login"))),
				),
				h.If(!loggedIn, h.Li(h.A(h.Href("/register"), h.Role("button"), h.Text("Register")))),
			),
		),
		h.Main(h.Class("container"), content),
	)
}

// Middleware: redirect to /login if no User in the session.
func requireAuth(w http.ResponseWriter, r *http.Request, next http.Handler) {
	if u, ok := sess.Get[User](r); !ok || u.Email == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	next.ServeHTTP(w, r)
}

func main() {
	app := via.New(
		via.WithTitle("Via Auth"),
		via.WithPlugins(picocss.Plugin(
			picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeAmber}),
		)),
	)

	// Public pages
	via.Mount[LandingPage](app, "/")
	via.Mount[LoginPage](app, "/login")
	via.Mount[RegisterPage](app, "/register")

	// Protected page
	protected := app.Group("")
	protected.Use(requireAuth)
	via.Mount[ProfilePage](protected, "/profile")

	_ = http.ListenAndServe(":3000", app)
}
