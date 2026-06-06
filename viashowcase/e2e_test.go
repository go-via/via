//go:build e2e

// E2E test for the viashowcase docker-compose cluster (Postgres + JetStream NATS
// + three app pods behind a sticky HAProxy load balancer). Tag-gated so it never
// runs in the normal `go test ./...` path — it needs Docker and builds images:
//
//	go test -tags e2e -run TestShowcase -timeout 15m .
//
// TestMain brings the stack up once and tears it down at the end; each test
// exercises a different cluster property. We drive the app by replaying Via's
// action protocol over raw net/http with a cookie jar: GET a page to scrape the
// via_tab id out of the HTML-escaped data-signals, then POST /_action/<Name>
// carrying {via_tab, ...signals}. GET+POST share the jar so the sticky LB pins
// both to the same pod.
package viashowcase_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// lb is the only public entrypoint: the sticky-cookie HAProxy balancer fronting
// app1/app2/app3. compose maps it to :3000.
const lb = "http://localhost:3000"

// tabRE mirrors vt's: the via_tab id is HTML-escaped into the page's data-signals.
var tabRE = regexp.MustCompile(`&#34;via_tab&#34;:&#34;([^"&]+)&#34;`)

func TestMain(m *testing.M) {
	if out, err := compose("up", "--build", "-d"); err != nil {
		fmt.Printf("compose up failed: %v\n%s\n", err, out)
		os.Exit(1)
	}

	code := 1
	if waitReady(lb, 5*time.Minute) {
		code = m.Run()
	} else {
		fmt.Println("cluster did not become ready")
	}

	if out, err := compose("down", "-v", "--remove-orphans"); err != nil {
		fmt.Printf("compose down failed: %v\n%s\n", err, out)
	}
	os.Exit(code)
}

// A vote cast through one path must converge to a fresh reader of the host view —
// over REAL JetStream, not InMemory. A host signs up, creates a poll, then a vote
// is cast against /r/{code}; the host big-screen page must reflect it.
func TestShowcaseVoteConvergesAcrossCluster(t *testing.T) {
	host := newJarClient(t)
	email := "host-" + nonce() + "@example.com"

	// Sign up the host (rotates a session into the jar) and create a poll.
	doAction(t, host, lb, "/signup", "Submit", map[string]any{
		"email": email, "password": "hunter2hunter2", "display": "Host " + nonce(),
	})
	choice := "blue-" + nonce()
	doAction(t, host, lb, "/", "Create", map[string]any{
		"title": "Favourite colour?", "kind": "poll", "choices": "red," + choice,
	})

	code := lastRoomCode(t, host)
	require.NotEmpty(t, code, "host should own at least one room after Create")

	// A fresh audience browser casts a vote through the join path (the choice
	// rides on the "draft" signal, as the poll buttons do client-side).
	voter := newJarClient(t)
	doAction(t, voter, lb, "/r/"+code, "Vote", map[string]any{"draft": choice})

	// The host big-screen streams the live tally over SSE (echarts), served from
	// whichever pod the host's sticky cookie pinned — likely a different pod than
	// the voter hit. Assert the choice converges INTO that live stream via the
	// JetStream backplane. (The tally is pushed over SSE, never in static HTML.)
	hostTab := tabOf(t, getBody(t, host, lb+"/host/"+code))
	require.True(t, sseContains(t, host, lb, hostTab, choice, 30*time.Second),
		"a vote on /r/{code} must converge into the /host/{code} live SSE stream across the cluster")
}

// The sticky LB must pin one cookie jar to a single pod across requests: HAProxy's
// VIA_LB cookie keeps every request from that jar on the same backend, otherwise
// the tab's SSE stream and its action POSTs would land on different pods.
func TestShowcaseStickyLBPinsAClient(t *testing.T) {
	browser := newJarClient(t)
	// Prime the jar so HAProxy sets its sticky cookie.
	getBody(t, browser, lb+"/")
	first := stickyCookie(t, browser)
	require.NotEmpty(t, first, "the LB must hand out a sticky VIA_LB cookie")

	for i := 0; i < 8; i++ {
		getBody(t, browser, lb+"/")
		require.Equal(t, first, stickyCookie(t, browser),
			"the LB must keep one browser pinned to the same pod (sticky cookie)")
	}
}

// compose runs `docker compose -f deploy/docker-compose.yml <args...>` from the
// module dir (the test's cwd).
func compose(args ...string) ([]byte, error) {
	full := append([]string{"compose", "-f", "deploy/docker-compose.yml"}, args...)
	return exec.Command("docker", full...).CombinedOutput()
}

// waitReady polls base until it serves the landing page or the deadline passes.
func waitReady(base string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && tabRE.Match(body) {
				return true
			}
		}
		time.Sleep(time.Second)
	}
	return false
}

// newJarClient returns an http.Client with its own cookie jar — one "browser".
// Redirects are not followed so we can assert the action's 200 directly.
func newJarClient(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// getBody GETs url with the given client and returns the page HTML.
func getBody(t *testing.T, hc *http.Client, url string) string {
	t.Helper()
	resp, err := hc.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// tabOf scrapes the via_tab id out of a rendered page's escaped data-signals.
func tabOf(t *testing.T, html string) string {
	t.Helper()
	m := tabRE.FindStringSubmatch(html)
	if len(m) < 2 {
		t.Fatalf("no via_tab id in page")
	}
	return m[1]
}

// doAction replays Via's action protocol: GET page (to mint a tab id + pick up
// the sticky cookie), then POST /_action/<name> with {via_tab, ...signals}. GET
// and POST share hc's jar so both reach the same pinned pod.
func doAction(t *testing.T, hc *http.Client, base, page, name string, signals map[string]any) {
	t.Helper()
	tab := tabOf(t, getBody(t, hc, base+page))

	payload := map[string]any{"via_tab": tab}
	for k, v := range signals {
		payload[k] = v
	}
	body, _ := json.Marshal(payload)

	resp, err := hc.Post(base+"/_action/"+name, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", name, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /_action/%s: status %d", name, resp.StatusCode)
	}
}

// roomCodeRE pulls a room short code out of a /host/{code} or /r/{code} link.
var roomCodeRE = regexp.MustCompile(`/(?:host|r)/([A-Za-z0-9]+)`)

// lastRoomCode scrapes the host's landing page ("your rooms") for the most recent
// room code it owns.
func lastRoomCode(t *testing.T, hc *http.Client) string {
	t.Helper()
	m := roomCodeRE.FindAllStringSubmatch(getBody(t, hc, lb+"/"), -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][1]
}

// stickyCookie returns the HAProxy sticky-balancing cookie value held by hc's jar.
func stickyCookie(t *testing.T, hc *http.Client) string {
	t.Helper()
	u, _ := url.Parse(lb)
	for _, c := range hc.Jar.Cookies(u) {
		if c.Name == "VIA_LB" {
			return c.Value
		}
	}
	return ""
}

// nonce returns a short, run-unique token.
func nonce() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }

// sseContains opens the host SSE stream for tab and reports whether needle shows
// up in any frame within the deadline. Uses a dedicated client with no overall
// timeout (the stream is long-lived) but shares hc's jar for session + stickiness.
func sseContains(t *testing.T, hc *http.Client, base, tab, needle string, within time.Duration) bool {
	t.Helper()
	ds := url.QueryEscape(`{"via_tab":"` + tab + `"}`)
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/_sse?datastar="+ds, nil)
	resp, err := (&http.Client{Jar: hc.Jar}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	var acc strings.Builder
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			acc.Write(buf[:n])
			if strings.Contains(acc.String(), needle) {
				return true
			}
		}
		if readErr != nil {
			return false
		}
	}
}
