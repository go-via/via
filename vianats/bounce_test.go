package vianats_test

import (
	"net"
	"net/http"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/go-via/vianats"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nuid"
	"github.com/stretchr/testify/require"
)

type visitsPage struct {
	Visits via.StateAppNum[int]
}

func (p *visitsPage) Bump(ctx *via.Ctx) error {
	// The error is swallowed on purpose: during the bounce a write can fail
	// against the unreachable server, and the test keeps bumping until one
	// commits — the assertion is on convergence, not on a single write.
	_ = p.Visits.Update(ctx, func(n int) (int, error) { return n + 1, nil })
	return nil
}

func (p *visitsPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.Span(h.ID("visits"), p.Visits.Text(ctx)))
}

var visitsRe = regexp.MustCompile(`<span id="visits">(\d+)</span>`)

// visitsCount extracts the rendered counter, or -1 when the span is absent.
func visitsCount(html string) int {
	m := visitsRe.FindStringSubmatch(html)
	if m == nil {
		return -1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return -1
	}
	return n
}

// A real-backend outage is a server bounce, not a polite channel close: the
// embedded JetStream server stops and restarts on the same port and store dir
// (streams, offsets, and the KV bucket survive), and cross-pod convergence
// must RESUME. The reconcile sweep is disabled, so only a surviving changes
// tailer can carry pod A's post-bounce write to pod B — a tailer that died
// with the first dropped subscription would fail this forever.
func TestJetStream_convergenceResumesAfterServerBounce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	srv := startBounceableServer(t, dir, -1)
	port := srv.Addr().(*net.TCPAddr).Port
	url := srv.ClientURL()
	prefix := "t" + nuid.Next()

	dial := func() *nats.Conn {
		nc, err := nats.Connect(url,
			nats.ReconnectWait(50*time.Millisecond),
			nats.MaxReconnects(-1),
		)
		require.NoError(t, err)
		t.Cleanup(nc.Close)
		return nc
	}
	bpA, err := vianats.JetStream(dial(), vianats.WithPrefix(prefix))
	require.NoError(t, err)
	bpB, err := vianats.JetStream(dial(), vianats.WithPrefix(prefix))
	require.NoError(t, err)

	// Insecure cookies: this module's go directive is >=1.25, where
	// net/http/cookiejar refuses to return Secure cookies for the plain-http
	// httptest origin — without the opt-out the SSE handshake 403s on a
	// session mismatch before the backplane is ever exercised.
	off := via.WithReconcileInterval(0)
	appA := via.New(via.WithBackplane(bpA), off, via.WithInsecureCookies())
	serverA := vt.Serve(t, appA)
	via.Mount[visitsPage](appA, "/")

	appB := via.New(via.WithBackplane(bpB), off, via.WithInsecureCookies())
	serverB := vt.Serve(t, appB)
	via.Mount[visitsPage](appB, "/")

	a := vt.NewClient(t, serverA, "/")
	b := vt.NewClient(t, serverB, "/")
	framesB, cancelB := b.SSEReady()
	defer cancelB()

	require.Equal(t, http.StatusOK, a.Action("Bump").Fire())
	vt.AwaitFrame(t, framesB, 10*time.Second, `<span id="visits">1</span>`)

	// Bounce: stop, then restart on the SAME port + store dir so the clients
	// reconnect and the JetStream state (offsets included) is recovered.
	srv.Shutdown()
	srv.WaitForShutdown()
	_ = startBounceableServer(t, dir, port)

	// Keep writing on A until B's render shows a post-bounce value (>1): the
	// hint rides the changes feed, so a fresh reader on B converging proves
	// B's tailer survived the outage. Repeated bumps are safe — every hint
	// makes B re-pull the Store HEAD, never a stale intermediate.
	require.Eventually(t, func() bool {
		_ = a.Action("Bump").Fire()
		return visitsCount(vt.NewClient(t, serverB, "/").HTML()) > 1
	}, 30*time.Second, 250*time.Millisecond,
		"pod B must converge again after the embedded server bounce")
}

// startBounceableServer boots an in-process JetStream server on the given
// port (-1 picks a free one) over dir, so a test can stop it and bring it
// back with identical identity — the file store is what lets streams and
// consumers outlive the bounce.
func startBounceableServer(t testing.TB, dir string, port int) *server.Server {
	t.Helper()
	opts := &server.Options{
		Port:      port,
		JetStream: true,
		StoreDir:  dir,
	}
	srv, err := server.NewServer(opts)
	require.NoError(t, err, "new embedded nats-server")
	go srv.Start()
	require.True(t, srv.ReadyForConnections(10*time.Second), "embedded nats-server not ready")
	t.Cleanup(srv.Shutdown)
	return srv
}
