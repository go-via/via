package content

import (
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/demo"
)

// Deploy is the operations page: what a running process holds, the order a
// shutdown must take, and what it takes to run more than one of it.
type Deploy struct{ page }

func NewDeploy(env Env) Deploy { return Deploy{page: newPage("/deploy", env)} }

func (p *Deploy) PageMeta() via.Meta {
	return p.meta("Shutdown order, sessions that survive a restart, sticky load balancing and a cross-pod topic bridge.")
}

func (p *Deploy) View() h.H {
	d := p.doc()
	return d.Page(
		h.P(h.Str("A via process holds two kinds of state. A session is a signed cookie plus a blob in a "+
			"SessionStore; it is the user's and it can follow them anywhere. A tab is a stream, the live children "+
			"behind it, their Tick timers and Listen subscriptions; it is the process's and it dies with it.")),

		d.H2("What a process holds"),
		table([]string{"State", "Where it lives", "Survives restart?"},
			[]h.H{h.Str("Session cookie"), h.Str("The browser, signed with the session key"),
				h.Str("Only with a fixed key: WithSessionKey or VIA_SESSION_KEY")},
			[]h.H{h.Str("Session data"), h.Str("The SessionStore; by default a map in this process"),
				h.Str("Only with a shared store: WithSessionStore")},
			[]h.H{h.Str("Tabs: streams, live children, State and List values"), h.Str("This process"),
				h.Str("No. The tab reloads and OnInit seeds it again")},
			[]h.H{h.Str("Tick timers, Listen subscriptions, topics"), h.Str("This process"),
				h.Str("No. OnInit registers them again on the next connect")},
		),

		d.H2("Shutdown order"),
		h.P(h.Str("The Router owns one goroutine per live tab. They hang off a context of the router's own, which "+
			"http.Server.Shutdown does not cancel, so close the router first or Shutdown waits on streams that "+
			"never end.")),
		demo.Code(`<-stop
r.Close()         // ends every stream cleanly, runs each OnDispose
srv.Shutdown(ctx) // then drains the plain requests`),
		h.P(h.Str("Close returns once the last stream goroutine is gone. An open stream ends the way a closed "+
			"tab ends, a clean end of response, not a truncated one. An action against a closing tab answers "+
			"410, which the client turns into a reload. A connect arriving after Close is refused 503, which "+
			"stops the client on a red banner, so a pod leaves the balancer before it calls Close. Calling "+
			"Close twice is fine.")),

		d.H2("Restarts"),
		h.P(h.Str("Two things have to survive a restart: the cookie and the data behind it. The cookie is signed, "+
			"not stored, so a stable key is all it needs. Unset, via reads VIA_SESSION_KEY and failing that mints a "+
			"random key per process, and every cookie is worthless the moment the process ends.")),
		h.P(h.Str("The data lives in the SessionStore, and the default one is a map in this process. With the key "+
			"alone a restart still logs everyone out. The interface is three methods over opaque bytes.")),
		demo.Code(`type redisSessions struct{ c *redis.Client }

func (r redisSessions) Load(ctx context.Context, id string) ([]byte, bool, error) {
	b, err := r.c.Get(ctx, "via:"+id).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	return b, err == nil, err
}

func (r redisSessions) Save(ctx context.Context, id string, data []byte, ttl time.Duration) error {
	return r.c.Set(ctx, "via:"+id, data, ttl).Err()
}

func (r redisSessions) Delete(ctx context.Context, id string) error {
	return r.c.Del(ctx, "via:"+id).Err()
}

via.NewRouter(via.WithSessionKey(key), via.WithSessionStore(redisSessions{c}))`),
		h.P(h.Str("Rotate is a Save under the new id then a Delete of the old. If the Save fails, or the old "+
			"id can be neither deleted nor expired, Rotate panics and the request answers 500 rather than report "+
			"a rotation that did not happen; a failed Save leaves the old session valid. Expiry is the ttl handed "+
			"to Save, "+
			"and via stamps the same deadline into the blob and refuses an expired Load, so a backend with no TTL "+
			"support is still correct; it only leaks dead rows. Behind a TLS-terminating proxy via sees plain "+
			"HTTP, so pass WithSecureCookies or the Secure attribute never gets set.")),

		d.H2("Horizontal scaling"),
		h.P(h.Str("With the key and the store above, the session follows the user to any pod. The tab does not. "+
			"An action POST carries a tab id and looks it up in the registry of the pod that opened the stream, so "+
			"an action landing on another pod answers 410 and the client reloads. A balancer needs affinity for "+
			"the life of a tab, not for correctness: a miss costs one reload.")),
		h.Ul(
			h.Li(h.Str("Pin on a cookie the balancer owns, not on via_session. That id changes on Rotate, which "+
				"would move a user to another pod in the middle of signing in.")),
			h.Li(h.Str("The stream is long-lived SSE over a plain POST: proxy buffering off, a read timeout past "+
				"your idle time, HTTP/1.1 to the upstream. No path needs special casing; the endpoints sit under "+
				"each mount.")),
			h.Li(h.Str("Roll one pod at a time: fail your readiness check, r.Close(), srv.Shutdown(). Tabs reload "+
				"and the balancer pins them elsewhere. via ships no health endpoint; the app owns one.")),
			h.Li(h.Str("The Content-Security-Policy is a pure function of the Head, so pods with different "+
				"session keys still serve identical policies.")),
		),

		d.H2("State across pods"),
		h.P(h.Str("A Topic fans out inside one process. Two pods are two brokers, and a Publish on one never "+
			"reaches a Track or Listen on the other. Nothing in via bridges them. Invert the write path: "+
			"handlers publish to your bus, and each pod runs one goroutine that feeds what it "+
			"hears into the local topic. Track and Listen do not change.")),
		demo.Code(`// per pod, at startup
go func() {
	sub := rdb.Subscribe(ctx, "room:"+room.id)
	for m := range sub.Channel() {
		var msg Message
		if json.Unmarshal([]byte(m.Payload), &msg) == nil {
			room.bus.Publish(msg)
		}
	}
}()

// the write path publishes outward, never to room.bus directly
func (r *Room) Post(ctx context.Context, msg Message) error {
	b, _ := json.Marshal(msg)
	return rdb.Publish(ctx, "room:"+r.id, b).Err()
}`),
		h.P(h.Str("Pub/sub is fire-and-forget, so a tab in the middle of a reload misses the gap. StateTrack calls "+
			"load again at every connect, so tracked state converges from the database whatever was missed; "+
			"only a Listen handler reacting to events can skip a beat. Derive presence from state rather than "+
			"counting arrivals and departures.")),

		d.H2("What a lost pod costs"),
		h.P(h.Str("Its open tabs go amber, then reload onto a pod that is still there. An action in flight on it is "+
			"gone, with no replay. Unsent client signal edits go with it. Sessions survive in the store. via does "+
			"nothing more for high availability.")),
	)
}
