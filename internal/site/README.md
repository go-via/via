# go-via.dev

The documentation site, written with via. Every demo on a page is a via child
actually running, and each one shows its own source.

## Run

```sh
cd internal/site
go run .
```

Then open <http://localhost:8080>.

## Environment

- `VIA_ADDR` — listen address, default `:8080`.
- `VIA_SESSION_KEY` — cookie signing key. Unset, the site mints a random key
  per boot and logs one line; every restart then logs visitors out.
- `VIA_ORIGIN` — the site's own origin (`https://go-via.dev` in production).
  Unset, via accepts action POSTs from any origin and says so at boot. Set it
  anywhere the site is reachable by others.

## Deploy (Ansible)

`deploy/` holds a playbook for one small VPS (Debian/Ubuntu): it cross-builds
the binary locally, installs it under systemd behind Caddy, and mints the
session key once. It needs Go and Ansible locally and SSH as root on the
server. There is no inventory file: pass the host inline, trailing comma and
all.

```sh
cd internal/site/deploy
ansible-playbook -i 203.0.113.10, playbook.yml
```

Same command redeploys. Point `go-via.dev` (and `www`) at the server first;
Caddy fetches the certificate on the first request. Override `domain`, `arch`
(`arm64` for ARM hosts) or `bind` with `-e`.

## Layout

- `main.go` — router options, one `via.Mount` per page, static file serving.
- `shell/` — the chrome every page renders into: header, sidebar, footer, the
  `via.Meta` helper, and `EnsureSession`.
- `content/` — one mounted root page per file.
- `demos/` — one live demo per file, embedded verbatim for its source pane.
- `demo/` — the card wrapper, chroma highlighting, and the resets and rate
  limits the public demos run under.
- `static/` — the committed CSS, JS, fonts and brand files, served from
  `embed`.

## Nested module, no build step

This is its own module (`go-via.dev/site`) with
`replace github.com/go-via/via => ../..`, so chroma stays out of via's
`go.mod` and the repository root build never sees it. `go build ./...` at the
root ignores this directory.

There is no asset pipeline and no Node. Source highlighting runs in-process at
startup.

`static/chroma.css` is the one generated file. Regenerate it only when the
chroma style or class prefix in `demo/source.go` changes, and commit the
result:

```sh
cd internal/site && go run ./demo/gen
```

## Adding a demo

1. Write `demos/<x>.go`: one type with a `View`, its actions as methods, and
   whatever hook it needs. The file is embedded whole and shown as the demo's
   source, so it has to read well on its own.
2. Add the type as a field on the content page's root struct.
3. Render it: `demo.Card(title, prose, via.Child(p.X), "<x>.go")`.

A demo that writes state other visitors see takes the page's `*demo.Limiter`
as a field, set from the page's `OnInit`, and checks `Lim.Allow(ctx)` before
mutating.

## Public-state policy

The shared demos are anonymous and world-writable, so they are bounded in two
ways:

- Shared lists keep the newest 50 entries and are wiped every 15 minutes; the
  reset is published through the demo's topic, so open tabs redraw instead of
  going stale.
- Every action that mutates shared state spends a token from a per-session
  bucket (20/min on `/live`, 30/min on `/actions`). Over the limit the handler
  returns without mutating and the UI says so.

Both key on `ctx.Session().ID()`, which is `""` until the visitor has a
session —
hence `shell.EnsureSession`, called first thing in those pages' `OnInit`.
