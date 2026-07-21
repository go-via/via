# Changelog

## v0.8.0 — the v2 core goes mainline

The rebuilt "bare core" replaces the v1 tree. Module path is now
`github.com/go-via/via` (no `/v2` suffix); v1 history is merged, the tree is
the v2 core.

### Breaking

v0.8 is a rebuild, not an incremental release: the v1 surface (plugins,
`h.Group`/`h.If` helpers, theme options, `WithoutSSEReconnect`,
`Session.Rotate`, the old composition types) is replaced wholesale by the
core below. Treat migration as a re-read of the README, not a diff.

- **`via/sess` merged into the root package**: `sess.Put`/`Get`/`Clear`/`Rotate`
  are now `via.SessPut`/`SessGet`/`SessClear`/`SessRotate`; the `sess`
  subpackage and its `internal/sessbridge` shim are gone.
- **Bare mutators**: `Signal.Set(v)`, `State.Set(v)`, `List.Append(v)` — no
  `ctx` argument. State is bare; ctx is for the request.
- **Composition is `via.Embed`**: child compositions are plain struct fields
  rendered with `via.Embed(p.Field)`. `Slot`, `Child[C]`, `NewChild`, `Fill`
  and the `.Embed` method are gone. Generic layouts: `Shell[C]{Body C}`.
- **`OnInit(*Ctx) error`**: the per-request hook now returns an error —
  `via.ErrNotFound` answers 404, anything else 500; the View never renders a
  lie. The same sentinel works from `OnConnect`.
- **Sessions are always on** (lazily — the cookie is only issued on the first
  write); the session options are tune-only. Key resolution:
  `WithSessionKey` → `VIA_SESSION_KEY` → random per-process key (warned at
  first mint).
- **Origin floor is open by default**; `WithTrustedOrigin` turns enforcement
  on (`WithInsecureOrigin` removed). The per-tab id remains the CSRF token.
- **`h` is elements + attributes + `Str` only**: the render plumbing
  (`Dyn`/`DynAttr`/`NewRenderer`/`Renderer`/`Binder`) moved behind
  `internal/hcore`.
- **One doorway each**: conditionals are `via.When` (no `If`), growing lists
  are `via.List[E]` (`State[[]E]` + `Append`), session rotation is
  `via.SessRotate`. Themes are your CSS, not options; the SSE reconnect manager
  is always on.

### Added

- **Full HTML5 vocabulary in `h`** (~105 constructors), minus the page-shell
  and footgun tags (`html`, `head`, `script`, `template`, …) — those stay
  via's. Typed `h.Href`/`h.Src`/`h.Action` attributes gate their URL through
  a `javascript:`/`data:` allowlist and neutralize to `#` loudly.
- **Router**: `via.NewRouter` + `via.Mount(r, "/path", Page{}, guards...)`
  serves a multi-page app behind one handler; `via.Register` is now literally
  `Mount` at `/` — one dispatch pipeline. Mounted pages carry the full live
  stack (SSE, live actions, islands).
- **Path params**: `via.Param[T](ctx, n)` reads the positional `{}` segment;
  a segment that doesn't decode is an honest 404 on every stateless
  transport.
- **`via.Listen[T](ctx, topic, handler)`**: subscribe + pump + auto-dispose
  in one line.
- **Arg events**: `via.OnClickArg` / `OnChangeArg` / `OnSubmitArg` carry a
  typed render-time datum with the event (no `OnInputArg` by design — an
  input's payload is a `Signal`).
- **Native forms + uploads**: `via.PostForm` (server-side submit + 303),
  `via.OnUpload` + `via.File` for multipart.
- **Redirect via boot CSP nonce**: a `@post` action's `via.Redirect` ships a
  `location.assign()` script stamped with `HMAC(key, "via/csp-nonce")` — a
  stateless nonce every document this app (or any pod sharing the key)
  serves already admits. Targets are gated by `h.SafeURL`; unsafe ones are
  dropped loudly with an element-patch fallback. Browser-verified under the
  strict CSP.
- **Resilience floor**: SSE keepalive comment frames (`WithSSEHeartbeat`),
  per-frame write deadlines (`WithSSEWriteTimeout`), half-open teardown, a
  client reconnect manager with a "Reconnecting…" banner and a capped
  reload-to-re-bootstrap (2), `WithMaxSSEConnections` (503 over the cap).

### Fixed

- Signal warnings: `Set` on a signal the View never rendered warns once
  instead of silently doing nothing.
- Session-cookie signature mismatch (two apps clobbering one cookie name)
  logs one loud diagnostic instead of silently resetting sessions.

Earlier releases (v0.7.0 and back) predate this changelog; see the git tags.
