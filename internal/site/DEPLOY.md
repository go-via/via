# Deploying go-via.dev

One static binary behind Caddy, on any Linux host with an init system. The
steps below are the contract; the service and Caddy config are given as
files you install by hand. Nothing here assumes a distribution.

## Layout on the host

- `/usr/local/bin/go-via-site` — the binary, mode 0755.
- `/etc/go-via.env` — `VIA_SESSION_KEY=<64 hex chars>`, mode 0600, owned by
  the service user. Minted once; rotating it logs every session out.
- `/etc/caddy/Caddyfile` — below.
- A system user `go-via`, no shell, no home. The service runs as it.
- Caddy from the distribution's package, pinned so an upgrade cannot move
  the proxy under the site.

The binary listens on `VIA_ADDR` (`127.0.0.1:8080`), Caddy terminates TLS
and proxies to it. Port 80 stays closed at the firewall: certificates come
over TLS-ALPN, and there is no plain-HTTP redirect to serve.

## 1. Build

From `internal/site`, on your machine. The version string is what `/healthz`
answers with, so stamp the tag or commit you are shipping:

```sh
cd internal/site
go vet ./... && go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags)" \
  -o .build/site-amd64 .
scp .build/site-amd64 user@host:stage/
```

Use `GOARCH=arm64` for an arm host. `.build/` is gitignored.

## 2. First install

As root on the host.

```sh
# Caddy from the distribution (apt, apk, dnf, pacman ...). Pin it.
# busybox: adduser -S -D -H -s /sbin/nologin go-via
useradd -r -M -s /sbin/nologin go-via
install -m 0755 stage/site-amd64 /usr/local/bin/go-via-site
umask 077
printf 'VIA_SESSION_KEY=%s\n' \
  "$(head -c32 /dev/urandom | od -An -tx1 | tr -d ' \n')" > /etc/go-via.env
chown go-via:go-via /etc/go-via.env
install -d -o caddy -g caddy -m 0750 /var/log/caddy
```

Then a service that:

- runs `/usr/local/bin/go-via-site` as `go-via`,
- sets `VIA_ADDR=127.0.0.1:8080`, `VIA_ORIGIN=https://go-via.dev` and
  `VIA_VERSIONS="v0.8=/,v0.7=https://go-via.github.io/via/"` (see
  [Versions](#versions)),
- sources `/etc/go-via.env`,
- restarts on exit with a short delay and no retry cap (a crash loop must
  keep trying rather than land the unit in failed-forever),
- gives the process 15 s to stop: the server drains live streams before it
  exits.

systemd:

```ini
[Unit]
Description=go-via.dev
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
ExecStart=/usr/local/bin/go-via-site
Environment=VIA_ADDR=127.0.0.1:8080
Environment=VIA_ORIGIN=https://go-via.dev
Environment="VIA_VERSIONS=v0.8=/,v0.7=https://go-via.github.io/via/"
EnvironmentFile=/etc/go-via.env
User=go-via
Restart=always
RestartSec=2
TimeoutStopSec=15
NoNewPrivileges=yes
CapabilityBoundingSet=
UMask=0077
ProtectSystem=strict
ProtectHome=yes
ProtectProc=invisible
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectHostname=yes
ProtectClock=yes
PrivateTmp=yes
PrivateDevices=yes
RemoveIPC=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=yes
RestrictSUIDSGID=yes
RestrictRealtime=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallFilter=@system-service
SystemCallErrorNumber=EPERM
SystemCallArchitectures=native

[Install]
WantedBy=multi-user.target
```

OpenRC: an `/etc/init.d/go-via` script with `supervisor=supervise-daemon`,
`respawn_delay=2`, `respawn_max=0`, `command_user=go-via:go-via`, and a
`start_pre` that sources `/etc/go-via.env` and exports the three variables.

Install the Caddyfile, validate it, enable both services, start the site
first and Caddy second.

```sh
caddy validate --adapter caddyfile --config /etc/caddy/Caddyfile
```

## 3. Check

```sh
wget -qO- http://127.0.0.1:8080/healthz   # ok <version>
wget -qO- https://go-via.dev/healthz
```

Open a page with a live demo and confirm the event stream stays open; a
proxy that buffers `text/event-stream` shows up here as a page that never
updates.

## 4. Redeploy

Build and copy as in step 1, then on the host:

```sh
install -m 0755 stage/site-amd64 /usr/local/bin/go-via-site
<restart go-via>          # systemctl restart go-via / rc-service go-via restart
wget -qO- http://127.0.0.1:8080/healthz
```

The restart drops every open stream; clients reconnect on their own. The
Caddyfile only changes when the proxy contract does; reload Caddy, do not
restart it, so in-flight TLS handshakes survive.

## Versions

The picker in the sidebar lists every version of the docs, and a page on an
older one carries a banner linking to the same page on the latest. Two
variables drive it:

- `VIA_BASE` — the path prefix this build is served under: empty for the
  latest, `/v0.8` for an older one. No trailing slash. Every mount, link,
  asset and action URL is built under it.
- `VIA_VERSIONS` — `label=base` pairs, comma-separated, latest first. A base
  is `/` for the root, `/vX.Y` for a prefix on this host, or an https URL for
  docs hosted elsewhere. Set the same string on every service; each build
  finds its own entry by `VIA_BASE` and refuses to start without one.

A build under a prefix sets `noindex` on its pages, so an old version does not
compete with the latest in search results, and names its session cookie after
the prefix (`via_session_v0_8`): the cookie is host-wide, and two versions
sharing one name would overwrite each other's session.

Each version is its own binary and service, built from that version's tag: a
second service `go-via-v0.8` on `127.0.0.1:8081` with `VIA_BASE=/v0.8`,
the same `VIA_ORIGIN`, `VIA_VERSIONS` and env file, and the binary at
`/usr/local/bin/go-via-site-v0.8`.

### Releasing a new version

1. Build the outgoing version from its tag and install it as the prefixed
   service (`VIA_BASE=/vX.Y`, next free port). Check its `/vX.Y/` page.
2. Add a `@vXY` block for it to the Caddyfile, validate, reload Caddy.
3. Put the new label first in `VIA_VERSIONS` on every service, the root one
   pointing at `/` and the outgoing one at `/vX.Y`, and restart them.
4. Redeploy the root service from the new tag as in [4. Redeploy](#4-redeploy).

### Caddy

Route each prefix to its own upstream, ahead of the default. Do not strip the
prefix: action and SSE URLs are derived from the prefixed mount, so the
upstream has to see the path the browser used.

```caddyfile
	@v08 path /v0.8 /v0.8/*
	handle @v08 {
		reverse_proxy 127.0.0.1:8081 {
			flush_interval -1
		}
	}
	handle {
		reverse_proxy 127.0.0.1:8080 {
			flush_interval -1
		}
	}
```

These `handle` blocks replace the single `reverse_proxy` in the site block
below.

## Caddyfile

```caddyfile
{
	# Port 80 is closed at the host firewall: TLS-ALPN only, no HTTP redirect.
	auto_https disable_redirects
}

go-via.dev {
	tls {
		issuer acme {
			disable_http_challenge
		}
	}

	header {
		Strict-Transport-Security "max-age=31536000; includeSubDomains"
		Referrer-Policy strict-origin-when-cross-origin
		Permissions-Policy "camera=(), microphone=(), geolocation=()"
		# The version of the proxy is not the visitor's business.
		-Server
	}

	# The site takes no uploads; via's own 8 MiB default never has to be reached.
	request_body {
		max_size 2MB
	}

	# text/* would also compress, and so buffer, text/event-stream.
	# .geojson's type comes from the host's mime table.
	encode zstd gzip {
		match {
			header Content-Type text/html*
			header Content-Type text/css*
			header Content-Type text/plain*
			header Content-Type text/javascript*
			header Content-Type application/javascript*
			header Content-Type application/json*
			header Content-Type application/geo+json*
			header Content-Type image/svg+xml*
		}
	}

	reverse_proxy 127.0.0.1:8080 {
		# SSE: never buffer the upstream stream.
		flush_interval -1
	}

	log {
		output file /var/log/caddy/go-via.log
	}
}

www.go-via.dev {
	tls {
		issuer acme {
			disable_http_challenge
		}
	}

	# The redirect is a response of its own, so it carries the HSTS header or
	# the www host never gets one.
	header Strict-Transport-Security "max-age=31536000; includeSubDomains"
	redir https://go-via.dev{uri} permanent
}
```
