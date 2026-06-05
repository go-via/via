# Cluster chat — the backplane across nodes

The single-node [`chat`](../chat) example is a live chatroom whose message log is
an app-scoped event log. This is the **same app**, run as several nodes over a
shared [backplane](https://go-via.github.io/via/distributed-state) (NATS
JetStream). A line typed against one node fans out to every tab on every *other*
node — that cross-node fan-out is what the backplane buys you.

A banner on each page shows which node served it, so you can watch state cross
between them.

## Run the whole cluster (Docker Compose)

Spins up JetStream NATS + two app nodes + a sticky-cookie load balancer:

```sh
docker compose -f internal/examples/chatcluster/docker-compose.yml up --build
```

Open the load balancer — it's the single entry point:

- via the LB → <http://localhost:3000>

Open it in **two separate browsers** (or one window + one incognito). Each gets
pinned to a node (possibly different ones), and messages still sync between them.
The banner tells you which node rendered each page. The nodes are also exposed
directly on <http://localhost:3001> and <http://localhost:3002> if you want to
bypass the balancer.

## Run a single node by hand

Against your own NATS server:

```sh
NATS_URL=nats://localhost:4222 PORT=3001 NODE_NAME=node-one \
  go run ./internal/examples/chatcluster
```

With `NATS_URL` unset it falls back to the in-process `via.InMemory()` backplane,
so it runs as an ordinary single node with no infrastructure.

## Why sticky, not plain round-robin?

Each tab's context (its SSE stream + action handler) is in-memory on the node
that served it, so a single tab must keep talking to **one** node. The backplane
converges the *shared state*, not the per-tab transport. A plain round-robin LB
would split one tab's SSE stream and action POSTs across nodes and break it.

So the bundled HAProxy (`haproxy.cfg`) is **sticky by cookie**: it inserts a
`VIA_LB` cookie that pins each browser to the node it first hit, while still
spreading *new* browsers across nodes. That's the supported way to scale Via
horizontally today — affinity for transport, backplane for state. (Two tabs in
the *same* browser share the cookie, so they land on the same node; use a second
browser to land on the other.)

## Configuration

| Env | Default | Meaning |
|---|---|---|
| `NATS_URL` | _(unset → InMemory)_ | NATS server URL; enables the durable JetStream backplane |
| `PORT` | `3000` | HTTP listen port |
| `NODE_NAME` | hostname | Identity shown in the banner |
