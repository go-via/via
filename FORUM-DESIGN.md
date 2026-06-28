# Forum app — a design-driving spike

This is **wishful code**: a complete forum (sign-up, sign-in, profile with avatar
upload, threads, posts) written in the API we *want*, then read backwards to
extract the features v2 still needs. None of it compiles yet — the `🆕` markers
are exactly the gaps. The goal is to let a concrete app dictate each feature's
shape before we build it.

Status of the primitives it uses:

- `via.Signal/State/List`, `OnClick/OnSubmit/OnClickArg`, `via.Child[C]`,
  `via/sess` (`Put/Get/Clear/Rotate`) — **exist today**.
- `🆕 router` (multi-page mount, positional path params, per-route guards,
  redirect), `🆕 multipart upload`, `🆕 OnInit` (per-request page hook) — **the
  gaps this spike surfaces**.

---

## 1. The app, page by page (wishful)

### Shared app-land (not framework)

```go
type User struct{ ID int; Email, Name, AvatarURL string }
type Thread struct{ ID int; Title, Author string }
type Post struct{ Author, Body string }

// Store is plain app data: users + bcrypt hashes, threads, posts, avatar blobs.
// createUser/authenticate/saveAvatar/threads/posts/reply live here — stdlib only
// (crypto/bcrypt-equivalent, an io.Reader for the file). The framework owns none
// of this; it only needs to hand the page a session, a path param, and a file.
type Store struct{ /* … */ }
```

### Sign-up `/signup`

```go
type SignUp struct {
	store *Store
	Email via.Signal[string]
	Name  via.Signal[string]
	Pass  via.Signal[string]
	err   string
}

func (s *SignUp) Submit(ctx *via.Ctx) {
	u, err := s.store.createUser(s.Email.Get(), s.Name.Get(), s.Pass.Get())
	if err != nil {
		s.err = err.Error() // re-renders the form with the error
		return
	}
	sess.Put(ctx, u)              // log in
	sess.Rotate(ctx)              // fixation defense (exists)
	via.Redirect(ctx, "/profile") // 🆕 an action redirects the browser
}

func (s *SignUp) View() h.H {
	return h.Form(via.OnSubmit(s.Submit),
		h.Input(s.Email.Bind(), h.RawAttr("type", "email")),
		h.Input(s.Name.Bind()),
		h.Input(s.Pass.Bind(), h.RawAttr("type", "password")),
		h.P(h.Str(s.err)),
		h.Button(h.Str("sign up")),
	)
}
```

`/login` is the same shape (`store.authenticate` → `sess.Put` → `Rotate` →
`Redirect`).

### Profile `/profile` — current user + avatar upload

```go
type Profile struct {
	store  *Store
	user   User           // loaded per request — see OnInit
	Name   via.Signal[string]
	Avatar via.Upload     // 🆕 file-upload handle
}

// 🆕 OnInit runs once per request, BEFORE the (ctx-free) View, so the page can
// load session/request data into its fields. Without it a stateless page cannot
// render "the logged-in user" — View has no ctx to call sess.Get from.
func (p *Profile) OnInit(ctx *via.Ctx) {
	p.user, _ = sess.Get[User](ctx)
	p.Name.SetInitial(p.user.Name) // seed the bound input with the current value
}

func (p *Profile) SaveName(ctx *via.Ctx) {
	p.user.Name = p.Name.Get()
	p.store.save(p.user)
	sess.Put(ctx, p.user)
}

// 🆕 OnUpload delivers the multipart file as a typed param — the file analogue of
// OnClickArg's value. f is read/stored app-side; the framework just parses
// multipart and hands it over.
func (p *Profile) SaveAvatar(ctx *via.Ctx, f via.File) {
	url, _ := p.store.saveAvatar(p.user.ID, f) // f is an io.Reader + filename + size
	p.user.AvatarURL = url
	p.store.save(p.user)
	sess.Put(ctx, p.user)
}

func (p *Profile) View() h.H {
	return h.Div(
		h.Img(h.RawAttr("src", p.user.AvatarURL)),                 // 🆕 h.Img
		h.Form(via.OnUpload(p.SaveAvatar), p.Avatar.Input(),       // 🆕 multipart form + file input
			h.Button(h.Str("upload"))),
		h.Form(via.OnSubmit(p.SaveName), h.Input(p.Name.Bind()),
			h.Button(h.Str("save name"))),
	)
}
```

### Forum `/forum` — thread list + new thread

```go
type Forum struct {
	store   *Store
	threads []Thread // loaded in OnInit
	New     via.Signal[string]
}

func (f *Forum) OnInit(ctx *via.Ctx) { f.threads = f.store.threads() }

func (f *Forum) Post(ctx *via.Ctx) {
	u, _ := sess.Get[User](ctx)
	f.store.newThread(u, f.New.Get())
	f.New.Set(ctx, "")
}

func (f *Forum) row(t Thread) h.H {
	return h.Li(h.A(h.Href("/thread/"+strconv.Itoa(t.ID)), h.Str(t.Title))) // 🆕 h.A/h.Href
}

func (f *Forum) View() h.H {
	return h.Div(
		h.Ul(via.Each(f.threads, f.row)),
		h.Form(via.OnSubmit(f.Post), h.Input(f.New.Bind()), h.Button(h.Str("new thread"))),
	)
}
```

### Thread `/thread/{}` — posts + reply, with a path param

```go
type Thread struct {
	ID    via.Path[int] // 🆕 positional path param, bound to the {} in the mount pattern
	store *Store
	posts []Post
	Reply via.Signal[string]
}

func (t *Thread) OnInit(ctx *via.Ctx) { t.posts = t.store.posts(t.ID.Get()) }

func (t *Thread) Send(ctx *via.Ctx) {
	u, _ := sess.Get[User](ctx)
	t.store.reply(t.ID.Get(), u, t.Reply.Get())
	t.Reply.Set(ctx, "")
}

func (t *Thread) View() h.H {
	return h.Div(
		h.Ul(via.Each(t.posts, func(p Post) h.H { return h.Li(h.B(h.Str(p.Author+": ")), h.Str(p.Body)) })),
		h.Form(via.OnSubmit(t.Send), h.Input(t.Reply.Bind()), h.Button(h.Str("reply"))),
	)
}
```

### Wiring `main`

```go
func main() {
	store := NewStore()
	app := via.NewRouter(via.WithSessionKey(key)) // 🆕 router; sessions configured on it

	app.Mount("/signup", SignUp{store: store}) // 🆕 Mount a page at a path
	app.Mount("/login", Login{store: store})

	// 🆕 a per-route guard: no session → redirect. RequireSession is a named
	// constructor (no closure at the call site); the redirect target is a URL.
	guard := via.RequireSession[User]("/login")
	app.Mount("/profile", Profile{store: store}, guard)
	app.Mount("/forum", Forum{store: store}, guard)
	app.Mount("/thread/{}", Thread{store: store}, guard) // 🆕 {} = positional Path slot

	http.Handle("/", app)
	http.ListenAndServe(":8080", nil)
}
```

---

## 2. What the spike forces (minimal designs)

### A. Router / multi-page (#6)

The spine. Everything else hangs off it.

```go
type Router struct{ /* … */ }
func NewRouter(opts ...Option) *Router            // sessions, theme, etc. configured here
func (r *Router) Mount[T any, PT *T+viewer](path string, root T, guards ...Guard)
func (r *Router) ServeHTTP(http.ResponseWriter, *http.Request) // it IS the handler
```

- **Mount** is `Register` generalized to a path instead of always `/{$}`. Same
  by-value root, same per-request copy, same action/SSE endpoints — but scoped
  under the mount path so two pages' `/_via/a/{n}` don't collide
  (`/profile/_via/a/0` vs `/forum/_via/a/0`).
- **Positional path params.** The pattern uses anonymous `{}` (not `{id}`) to
  honor *no identifier strings* — params bind positionally, exactly like actions
  and signals. A `via.Path[T]` value-field handle reads its segment:

  ```go
  type Path[T any] struct{ /* … */ } // T is string/int/… ; decoded from the URL segment
  func (p *Path[T]) Get() T
  ```

  Mount walks the pattern's `{}` slots and the root's `Path[T]` fields in order.
- **Guards** = `type Guard func(*Ctx) (redirect string, ok bool)` produced by
  named constructors like `RequireSession[T](loginPath)`. Run before `OnInit`;
  a non-empty redirect short-circuits the page. (This is "middleware" without a
  closure-taking `Group(fn)` — guards are values passed to `Mount`.)
- **Redirect** from an action: `via.Redirect(ctx, path)` emits a Datastar
  `location` patch (or a 303 on a non-Datastar submit). Needed by sign-up/in.

Open questions: per-mount sub-mux vs one shared mux with path-prefixed action
ids; how the SSE `/_via/sse` per-page stream is namespaced under a mount; whether
`Path[T]` decode failure is a 404 vs a bound zero.

### B. Multipart file upload (#9)

```go
type Upload struct{ /* … */ }          // value-field handle
func (u *Upload) Input() h.Attr        // renders <input type=file> + makes the form multipart
type File interface {                  // what the handler receives
	io.Reader
	Name() string
	Size() int64
	ContentType() string
}
func OnUpload(fn func(*Ctx, File)) h.Attr // form binding; the file analogue of OnClickArg
```

- `OnUpload` is the value-carrying-action idea extended: instead of a JSON value
  in the query, the form is `enctype=multipart/form-data` and the file rides the
  body. The handler gets a typed `File`, same as `OnClickArg` gets a typed value.
- This is the one place that **breaks the Datastar `@post` JSON model** — a file
  needs a real multipart submit. So `OnUpload`'s form posts natively (not via
  Datastar) to a `/_via/upload/{n}` endpoint that parses multipart under a size
  cap and either redirects or returns an element-patch. Decide: native form vs
  Datastar `FormData` support.

Open questions: size cap + streaming vs buffering; where blobs live (the
framework should NOT own storage — `File` is just an `io.Reader` the app drains);
how the post-upload response patches the page (redirect vs `#root` morph).

### C. Auth — assembled, mostly on what exists

No new framework primitive beyond the guard (above) and `Redirect`. The flow:

- `store.createUser/authenticate` (bcrypt) — **app-land**, stdlib.
- `sess.Put(ctx, user)` on success, `sess.Rotate(ctx)` for fixation — **exist**.
- `RequireSession[User]("/login")` guard on protected mounts — **from #6**.
- "current user" in a page = `sess.Get[User](ctx)` inside `OnInit`.

The only framework gaps auth reveals are the **guard** and **`OnInit`** — both
fall out of the router work.

### D. `OnInit` — the per-request page hook (surprise gap)

A stateless page's `View` is **ctx-free** by guarantee, so it cannot call
`sess.Get` / read the path / load DB rows. Today that's fine (counter, poll get
everything from injected stores). But a forum page must render *the logged-in
user* and *this thread's posts* — request-scoped data. So pages need a hook that
runs with a `Ctx` **before** `View`, to load that data into fields:

```go
type Initer interface{ OnInit(*Ctx) }
```

`Mount` calls `OnInit` (if present) after guards, before render — the stateless
mirror of `OnConnect` for live islands. This is the cleanest gap the spike
exposed: without it, sessions + path params are reachable in *actions* but not at
*first paint*.

---

## 3. Proposed build order

1. **Router core** (`NewRouter`, `Mount`, path-prefixed action/SSE endpoints) +
   **`OnInit`** + **`Redirect`** — unlocks every multi-page flow and auth. Largest
   single piece; everything depends on it.
2. **`RequireSession` guard + positional `Path[T]`** — completes routing for the
   protected pages and the thread route.
3. **Multipart upload** (`Upload`/`Input`/`OnUpload`/`File`) — the avatar; the
   one piece that steps outside the Datastar JSON model.
4. Assemble `example/forum` for real (auth + threads/posts via `Each` +
   `OnClickArg` for per-post delete) — and it becomes the integration test that
   proves the three features compose.

Smallest-surface bias throughout (the `OnClickArg` lesson): prefer one value-/
file-carrying primitive over a subsystem; prefer positional `{}`/`Path[T]` over
named string params; guards-as-values over closure-taking groups.
