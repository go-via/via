package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/shell"
	"go-via.dev/site/snippet"
)

// Deploy is the operations page: the settings production needs, the proxy and
// service config around the binary, the order a shutdown must take, and what
// it takes to run more than one process.
type Deploy struct{ page }

func NewDeploy(env Env) Deploy { return Deploy{page: newPage("/deploy", env)} }

func (p *Deploy) PageMeta() via.Meta {
	return p.meta("Production checklist, Caddy and nginx config, timeouts, readiness, shutdown order, " +
		"rolling deploys, shared sessions and a cross-pod topic bridge.")
}

const deployCaddyfile = `example.com {
	# text/* would also compress, and so buffer, text/event-stream.
	encode zstd gzip {
		match {
			header Content-Type text/html*
			header Content-Type text/css*
			header Content-Type text/plain*
			header Content-Type text/javascript*
			header Content-Type application/javascript*
			header Content-Type application/json*
			header Content-Type image/svg+xml*
		}
	}

	reverse_proxy 127.0.0.1:8080 {
		# SSE: never buffer the upstream stream.
		flush_interval -1
	}
}`

const deployNginx = `# In the http block: open requests per client address, each open stream
# included. Clients behind one NAT share an address, so leave headroom.
limit_conn_zone $binary_remote_addr zone=perclient:10m;

server {
	listen 443 ssl;
	http2 on;
	server_name example.com;
	ssl_certificate     /etc/ssl/example.com/fullchain.pem;
	ssl_certificate_key /etc/ssl/example.com/privkey.pem;

	location / {
		limit_conn perclient 50;
		proxy_pass http://127.0.0.1:8080;
		proxy_http_version 1.1;
		proxy_set_header Connection "";
		proxy_set_header Host $host;
		proxy_set_header X-Forwarded-Proto $scheme;
		# SSE: pass each frame on as it arrives.
		proxy_buffering off;
		# Must exceed via's fixed 25 s keepalive.
		proxy_read_timeout 60s;
	}
}`

const deploySystemd = `[Unit]
Description=myapp
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
ExecStart=/usr/local/bin/myapp
Environment=VIA_ADDR=127.0.0.1:8080
Environment=VIA_ORIGIN=https://example.com
EnvironmentFile=/etc/myapp.env
User=myapp
Restart=always
RestartSec=2
TimeoutStopSec=15
NoNewPrivileges=yes
CapabilityBoundingSet=
UMask=0077
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
SystemCallFilter=@system-service
SystemCallArchitectures=native

[Install]
WantedBy=multi-user.target`

const deployOpenRC = `#!/sbin/openrc-run
description="myapp"

supervisor=supervise-daemon
command=/usr/local/bin/myapp
command_user=myapp:myapp
respawn_delay=2
respawn_max=0
output_log=/var/log/myapp.log
error_log=/var/log/myapp.log

depend() {
	need net
	before caddy
}

start_pre() {
	set -a
	. /etc/myapp.env
	set +a
	export VIA_ADDR=127.0.0.1:8080
	export VIA_ORIGIN=https://example.com
	checkpath -f -o myapp:myapp -m 0600 /var/log/myapp.log
}`

func (p *Deploy) View() h.H {
	d := p.doc()
	return d.Page(
		h.P(h.Str("One binary behind a TLS-terminating proxy. Set these before it faces the internet; "+
			"the proxy, service unit and multi-instance notes follow.")),

		d.H2("Production checklist"),
		table([]string{"Setting", "Set it with", "Left unset"},
			[]h.H{h.Str("Session key"),
				h.Span(Code("VIA_SESSION_KEY"), h.Str(" or "), API("via.WithSessionKey"),
					h.Str(", at least 16 bytes. Generate with "), Code("openssl rand -hex 32"), h.Str(".")),
				h.Str("A random key per process: every cookie dies on restart and no other pod accepts it.")},
			[]h.H{h.Str("Session store"), API("via.WithSessionStore"),
				h.Str("A map in this process. A restart logs everyone out even with a fixed key.")},
			[]h.H{h.Str("Trusted origin"), API("via.WithTrustedOrigin"),
				h.Str("Fails open: actions accept requests from every origin, including ones that carry no " +
					"origin signal at all. via logs one warning at startup.")},
			[]h.H{h.Str("Secure cookies"), API("via.WithSecureCookies"),
				h.Str("Secure follows TLS or the proxy's X-Forwarded-Proto. Unneeded behind Caddy, or nginx " +
					"configured as below.")},
			[]h.H{h.Str("Stream cap"), API("via.WithMaxSSEConn"),
				h.Str("10000 streams per router; the next connect answers 503.")},
			[]h.H{h.Str("Per-client limits"), h.Str("The proxy, below"),
				h.Str("One client can open streams until the router-wide cap refuses everyone.")},
			[]h.H{h.Str("Idle timeouts"), h.Span(h.Str("Proxy and balancer, above 25 s; "), API("via.WithPinnedDeadline"),
				h.Str(" below the balancer's request timeout")),
				h.Str("The proxy cuts idle streams, or answers a stuck action before via can.")},
			[]h.H{h.Str("Readiness probe"), h.Str("Your own handler, below"),
				h.Str("The balancer keeps sending new tabs to a pod that is shutting down.")},
		),
		snippet.Region("deploy/main.go", "router", snippet.Title("main.go"),
			snippet.Mark("WithTrustedOrigin", "WithSecureCookies", "WithSessionStore")),
		h.P(h.Str("via uses the bytes of "), Code("VIA_SESSION_KEY"), h.Str(" as they are; it does not "+
			"hex-decode them. The 64 characters "), Code("openssl rand -hex 32"), h.Str(" prints are a 64-byte "+
			"key, and a key under 16 bytes panics when the router is built. Rotating the key logs every "+
			"session out.")),
		Callout(Note, "Only VIA_SESSION_KEY is via's",
			h.P(h.Str("via reads one environment variable. "), Code("VIA_ORIGIN"), h.Str(" and "), Code("VIA_ADDR"),
				h.Str(" are read by the "), Code("main.go"), h.Str(" on this page, and the unit files below "+
					"set them for it. Why the origin check and Secure cookie matter is in "),
				h.A(h.Href(d.Href("/security")+"#origin-checks-and-csrf"), h.Str("Origin checks and CSRF")), h.Str("."))),

		d.H2("Reverse proxy"),
		snippet.Text("Caddyfile", deployCaddyfile),
		snippet.Text("nginx.conf", deployNginx),
		h.P(h.Str("A tab's stream is one long "), Code("text/event-stream"), h.Str(" response to a POST. A proxy "+
			"that buffers or compresses it holds every patch back, and the page never updates while clicks "+
			"still POST. Do not strip a path prefix: action and stream URLs sit under each mount, so the "+
			"upstream has to see the path the browser used. With nginx, forward "), Code("Host"), h.Str(" and "),
			Code("X-Forwarded-Proto"), h.Str(": without a trusted origin match, via's same-origin check compares the "+
				"browser's Origin with Host, and the proto decides whether the session cookie is Secure. Caddy sends both "+
				"as is.")),

		d.H2("Limits and dead peers"),
		h.P(h.Str("Limit connections and request rates per client at the proxy: via sees only the proxy's address. nginx does it with "),
			Code("limit_conn"), h.Str(" (above) and "), Code("limit_req"), h.Str(". Stock Caddy has neither; build in the "),
			Code("rate_limit"), h.Str(" module with "), Code("xcaddy"), h.Str(", or limit at the balancer or firewall in front.")),
		h.P(h.Str("Behind a proxy, via's 25 s keepalive detects a dead proxy, not a dead browser. A browser that vanishes "+
			"without closing its connection is the proxy's to notice, through a failed write or TCP keepalive (Caddy's "),
			Code("keepalive_interval"), h.Str("). The proxy then closes the upstream request, and via ends the tab's stream.")),

		d.H2("Timeouts"),
		snippet.Region("deploy/main.go", "server", snippet.Title("main.go"), snippet.Mark("WriteTimeout")),
		table([]string{"Timer", "Value", "What it means for you"},
			[]h.H{h.Str("Keepalive frame"), h.Str("25 s, fixed"),
				h.Str("The longest a healthy stream stays silent. Every proxy and balancer idle timeout on " +
					"the path must be longer: 60 s is enough.")},
			[]h.H{h.Str("Frame write"), h.Str("10 s, fixed"),
				h.Str("A peer that stops reading loses its stream after this.")},
			[]h.H{h.Str("Pinned deadline"), h.Span(h.Str("5 s, "), API("via.WithPinnedDeadline")),
				h.Str("How long an action waits, in all, for a stream still connecting (410 if it never comes) and for " +
					"its tab's goroutine to pick it up (503). Keep it under the balancer's request timeout so the answer is via's.")},
			[]h.H{h.Code(h.Str("http.Server.WriteTimeout")), h.Str("0"),
				h.Str("It bounds the whole response, and a stream is one response for the life of the tab.")},
		),
		h.P(h.Str("The keepalive is the only way via notices a peer that vanished without closing the "+
			"connection, which is why it cannot be turned off or slowed down. Behind a proxy that peer is the "+
			"proxy; see "), h.A(h.Href("#limits-and-dead-peers"), h.Str("Limits and dead peers")), h.Str(".")),

		d.H2("Health and readiness"),
		snippet.Region("deploy/main.go", "health", snippet.Title("main.go")),
		h.P(h.Str("via ships no health endpoint; the app owns both. "), Code("/healthz"), h.Str(" answers while "+
			"the process is up. "), Code("/readyz"), h.Str(" answers 503 from the moment shutdown starts, so "+
			"the balancer stops sending new tabs to this pod before its streams close. Mount them on a mux in "+
			"front of the router so a probe never touches sessions.")),

		d.H2("Shutdown order"),
		snippet.Region("deploy/main.go", "shutdown", snippet.Title("main.go"), snippet.Mark("r.Shutdown(shut)")),
		Steps(
			Step("Fail readiness, then wait",
				h.P(h.Str("Long enough for the balancer to mark the pod down: probe interval times failure "+
					"threshold."))),
			Step("Shut the router down",
				h.P(API("via.Router.Shutdown"), h.Str(" ends every stream the way a closed tab ends, with a clean "+
					"end of response, and runs each "), API("via.Ctx.OnDispose"), h.Str(". It returns once the "+
					"last stream goroutine is gone. An action in flight answers normally or 410; one still waiting "+
					"for its stream, and a connect after it, answer 503. Calling it twice is fine.")),
				h.P(h.Str("A handler blocked in your code cannot be stopped from outside, so its stream ends only "+
					"when the handler returns. If the deadline comes first, Shutdown returns the context's error "+
					"and logs the tabs still blocked. "),
					API("via.Router.Close"), h.Str(" is Shutdown with no deadline."))),
			Step("Shut the server down",
				h.P(h.Code(h.Str("http.Server.Shutdown")), h.Str(" drains the plain requests, under the same "+
					"deadline. It does not cancel the router's streams: called first, it waits on tabs that "+
					"never end."))),
		),
		h.P(h.Str("Five seconds of drain and five shared by both Shutdowns fit inside the 15 s the systemd unit "+
			"below gives the process to stop ("), Code("TimeoutStopSec"), h.Str(").")),

		d.H2("Service unit"),
		snippet.Text("/etc/systemd/system/myapp.service", deploySystemd),
		snippet.Text("/etc/init.d/myapp", deployOpenRC),
		h.P(h.Str("Both run the binary as its own user, source "), Code("/etc/myapp.env"), h.Str(" (mode 0600, "+
			"one line: "), Code("VIA_SESSION_KEY=…"), h.Str("), and restart on exit after 2 s with no retry "+
			"cap, so a crash loop keeps trying instead of parking the unit as failed. They mirror the files "+
			"go-via.dev itself runs under; "),
			shell.ExtLink(repo+"internal/site/DEPLOY.md", h.Str("DEPLOY.md")),
			h.Str(" has the full hardening list and the first-install steps.")),

		d.H2("What a process holds"),
		table([]string{"State", "Where it lives", "Survives restart?"},
			[]h.H{h.Str("Session cookie"), h.Str("The browser, signed with the session key"),
				h.Span(h.Str("Only with a fixed key: "), API("via.WithSessionKey"), h.Str(" or "),
					Code("VIA_SESSION_KEY"))},
			[]h.H{h.Str("Session data"), h.Span(h.Str("The "), API("via.SessionStore"), h.Str("; by default a map in this process")),
				h.Span(h.Str("Only with a shared store: "), API("via.WithSessionStore"))},
			[]h.H{h.Span(h.Str("Tabs: streams, live units, "), API("via.State"), h.Str(" and "), API("via.List"), h.Str(" values")),
				h.Str("This process"),
				h.Str("No. The tab reloads and OnInit seeds it again")},
			[]h.H{h.Span(API("via.Ctx.Tick"), h.Str(" timers, "), API("via.Ctx.Listen"), h.Str(" subscriptions, topics")),
				h.Str("This process"),
				h.Str("No. OnInit registers them again on the next connect")},
		),

		d.H2("Rolling deploys"),
		h.P(h.Str("When a pod closes, each of its open tabs sees its stream end. via's client shows a "+
			"Disconnected banner, polls the page URL with backoff from 500 ms to 8 s, and reloads once it "+
			"answers. The reload is a fresh GET: "), Code("OnInit"), h.Str(" runs again, "), API("via.State"),
			h.Str(" and signal values start from their seeds, and the session carries over if the store is "+
				"shared. An action against a tab the server no longer knows answers 410, and the client "+
				"reloads the same way.")),
		Callout(Warning, "A connect refused 503 does not retry",
			h.P(h.Str("A tab whose stream connect lands on a closed router, or one past "), API("via.WithMaxSSEConn"),
				h.Str(", stops on the banner with a Reconnect button and waits for the user. Fail readiness "+
					"before "), API("via.Router.Shutdown"), h.Str(" so reloading tabs land on a pod that is "+
					"staying up."))),
		h.P(h.Str("Roll one pod at a time. Unsent client edits and any action in flight on the closing pod are "+
			"lost; there is no replay.")),

		d.H2("Sessions that survive a restart"),
		snippet.Region("deploy/store.go", "store", snippet.Title("store.go")),
		h.P(h.Str("A "), API("via.SessionStore"), h.Str(" is three methods over opaque bytes. Against Redis "+
			"they are GET, SET with an expiry, and DEL. Implement "), API("via.VersionedSessionStore"),
			h.Str(" as well if the backend can make a write conditional on the revision it read.")),
		h.P(h.Str("Rotate is a Save under the new id, then a Delete of the old. If the Save fails, or the old id "+
			"can be neither deleted nor expired, "), API("via.Session.Rotate"), h.Str(" panics and the request "+
			"answers 500 rather than report a rotation that did not happen. Expiry is the ttl handed to Save, "+
			"and via stamps the same deadline into the blob and refuses an expired Load, so a backend with no "+
			"TTL support is still correct; it only keeps dead rows until you delete them.")),

		d.H2("Horizontal scaling"),
		h.P(h.Str("With a shared key and store, the session follows the user to any pod. The tab does not. An "+
			"action POST carries a tab id and looks it up on the pod that opened the stream, so an action "+
			"landing on another pod answers 410 and the tab reloads. A balancer needs affinity for the life of "+
			"a tab to avoid that reload; correctness does not depend on it.")),
		Callout(Caveat, "Pin on a cookie the balancer owns",
			h.P(h.Str("Not on "), Code("via_session"), h.Str(": its value changes on "), API("via.Session.Rotate"),
				h.Str(", which would move a user to another pod in the middle of signing in."))),

		d.H2("State across pods"),
		snippet.Region("deploy/bridge.go", "bus", snippet.Title("bridge.go")),
		snippet.Region("deploy/bridge.go", "bridge", snippet.Title("bridge.go"), snippet.Mark("r.Local.Publish", "r.Bus.Publish")),
		h.P(h.Str("A "), API("topic.Topic"), h.Str(" fans out inside one process. Two pods are two brokers, and a "+
			"publish on one never reaches a listener on the other; via has no bridge of its own. Invert the "+
			"write path: handlers publish to your bus, and each pod runs one goroutine that feeds what it hears "+
			"into the local topic.")),
		snippet.Region("deploy/bridge.go", "listen", snippet.Title("bridge.go"), snippet.Mark("c.Room.Local")),
		h.P(h.Str("Units keep listening to the local topic with "), API("via.Ctx.Listen"), h.Str(" or "),
			API("via.StateTrack"), h.Str(" and do not change. "), Code("Bus"), h.Str(" is a few lines over "+
				"go-redis ("), Code("Publish(…).Err()"), h.Str(", "), Code("Subscribe(…).Channel()"),
			h.Str(") or nats.go ("), Code("Publish"), h.Str(", "), Code("ChanSubscribe"), h.Str(").")),
		Callout(Caveat, "Pub/sub drops what a reloading tab misses",
			h.P(API("via.StateTrack"), h.Str(" calls its load again at every connect, so tracked state converges "+
				"from the database whatever was missed. A "), API("via.Ctx.Listen"), h.Str(" handler reacting to "+
				"events can skip a beat: derive presence from state rather than counting arrivals and "+
				"departures."))),
	)
}
