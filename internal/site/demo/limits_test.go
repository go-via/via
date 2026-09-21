package demo_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/demo"
)

// limited spends a token per GET. OnInit is where the Ctx a Limiter needs is
// reachable without an action address to POST to; the page mints no session,
// so every request shares the "" bucket.
type limited struct {
	lim *demo.Limiter
	ok  bool
}

func (p *limited) OnInit(ctx *via.Ctx) error {
	p.ok = p.lim.Allow(ctx)
	return nil
}

func (p *limited) View() h.H {
	return h.Div(h.Str("spent=" + strconv.FormatBool(p.ok)))
}

func limiterServer(t *testing.T, perMinute int) *httptest.Server {
	t.Helper()
	app := via.NewRouter()
	t.Cleanup(app.Close)
	via.Mount(app, "/", limited{lim: demo.NewLimiter(perMinute)})

	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

func spend(t *testing.T, srv *httptest.Server) bool {
	t.Helper()
	resp, err := http.Get(srv.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return strings.Contains(string(body), "spent=true")
}

// drain spends until the bucket refuses, within a budget generous enough that
// the refill during the loop cannot keep it going.
func drain(t *testing.T, srv *httptest.Server, perMinute int) {
	t.Helper()
	for range perMinute * 3 {
		if !spend(t, srv) {
			return
		}
	}
	require.Fail(t, "the limiter never refused within three times its budget")
}

func TestNewLimiter_panicsOnANonPositiveBudget(t *testing.T) {
	t.Parallel()

	for _, perMinute := range []int{0, -1} {
		t.Run(strconv.Itoa(perMinute), func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, func() { demo.NewLimiter(perMinute) })
		})
	}
}

func TestLimiter_refusesOnceTheBudgetIsSpent(t *testing.T) {
	t.Parallel()
	srv := limiterServer(t, 120)

	require.True(t, spend(t, srv), "a fresh bucket starts full")
	drain(t, srv, 120)

	assert.False(t, spend(t, srv))
}

func TestLimiter_allowsAgainOnceTheBucketRefills(t *testing.T) {
	t.Parallel()
	srv := limiterServer(t, 120)

	drain(t, srv, 120)
	// 120 a minute is two a second, so 700ms buys one back with margin.
	time.Sleep(700 * time.Millisecond)

	assert.True(t, spend(t, srv))
}

func TestLimiter_allowsConcurrentCallers(t *testing.T) {
	t.Parallel()
	srv := limiterServer(t, 10000)

	var wg sync.WaitGroup
	refused := make([]bool, 50)
	for i := range refused {
		wg.Add(1)
		go func() {
			defer wg.Done()
			refused[i] = !spend(t, srv)
		}()
	}
	wg.Wait()

	assert.NotContains(t, refused, true, "50 callers spent 50 of a 10000-a-minute budget")
}

func TestLimiter_keepsAnActiveBucketAcrossASweep(t *testing.T) {
	t.Parallel()
	srv := limiterServer(t, 1)

	require.True(t, spend(t, srv))
	// The sweep runs every 256th call; a bucket in use is not idle, so the
	// budget must survive one.
	for range 300 {
		spend(t, srv)
	}

	assert.False(t, spend(t, srv))
}
