# go-via.dev

The documentation site, written with via. Every demo is a via child actually
running, and each shows its own source.

## Run

```sh
cd internal/site
go run .
```

## Environment

- `VIA_ADDR` — listen address, default `:8080`.
- `VIA_SESSION_KEY` — cookie signing key, read by via. Under 32 bytes panics;
  unset mints a random key per boot, logging visitors out on restart.
- `VIA_ORIGIN` — the site's origin (`https://go-via.dev`). Also turns on
  Secure cookies, so set it only behind HTTPS. Unset accepts action POSTs from
  any origin and warns at boot.

`/healthz` answers `ok <version>`; the playbook stamps the short commit hash
via `-ldflags "-X main.version=..."`.

## Deploy

`deploy/playbook.yml` cross-builds locally, installs under systemd behind
Caddy, mints the session key once, and ends with a `/healthz` probe. Needs a
Debian-family host reachable as root, DNS for `domain` and `www.domain`
already pointing at it, and ansible-core 2.15+.

```sh
cd internal/site/deploy
ansible-playbook -i 203.0.113.10, playbook.yml
```

Override `domain` or `bind` with `-e`.

## Layout

- `main.go` — router options, mounts, static serving.
- `shell/` — chrome: `layout.go`, `nav.go` (`Nav`, `NavFor`), `meta.go`,
  `session.go`.
- `content/` — one mounted page per file.
- `demos/` — one demo per file, embedded verbatim for its Source tab.
  `shared_contract.go` holds what the demos use without declaring.
- `demo/` — the card, chroma highlighting, rate limits and resets.
- `static/` — CSS, JS, fonts, brand, served from `embed`.

This is a nested module (`go-via.dev/site`, `replace ../..`) so chroma stays
out of via's `go.mod`; the root `go build ./...` does not see it. No Node, no
asset pipeline. `static/chroma.css` is generated: `go generate ./demo`, and a
test pins it.

## Adding a demo

1. `demos/<x>.go`: one type with `View`, actions as methods. It is shown whole,
   so it has to read on its own.
2. A field on the page's root struct in `content/`.
3. `demo.Card(title, prose, via.Child(p.X), "<x>.go")`.

A demo that mutates shared state takes a `Limiter` field, set by the page's
constructor, and checks `Lim.Allow(ctx)` first.

## Public-state bounds

- The shared counter resets every 15 minutes, published so open tabs redraw.
- Per-tab lists cap at `keepRows` (50) and are never reset.
- Mutating actions spend a per-session token: 20/min on `/live`, 30/min on
  `/actions`. Sessions are minted by `shell.EnsureSession` so the bucket key
  is not `""`.
