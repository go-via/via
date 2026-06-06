//go:build e2e

// E2E test for the docker-compose cluster. It is tag-gated so it never runs in
// the normal `go test ./...` path — it needs Docker and builds images. Run it
// explicitly:
//
//	go test -tags e2e -run TestCluster -timeout 10m ./internal/examples/chatcluster
//
// TestMain brings the whole stack up once (JetStream NATS + node-one + node-two
// + the sticky HAProxy load balancer) and tears it down at the end; each test
// exercises a different property of it.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	nodeOne = "http://localhost:3001" // node-one, direct (bypasses the LB)
	nodeTwo = "http://localhost:3002" // node-two, direct
	lb      = "http://localhost:3000" // the sticky-cookie load balancer
)

// tabRE mirrors vt's: the via_tab id is HTML-escaped into the page's data-signals.
var tabRE = regexp.MustCompile(`&#34;via_tab&#34;:&#34;([^"&]+)&#34;`)

// bannerRE pulls the serving node name out of the rendered banner.
var bannerRE = regexp.MustCompile(`served by (node-[a-z]+)`)

func TestMain(m *testing.M) {
	if out, err := compose("up", "--build", "-d"); err != nil {
		fmt.Printf("compose up failed: %v\n%s\n", err, out)
		os.Exit(1)
	}
	ready := waitReady(nodeOne, 5*time.Minute) &&
		waitReady(nodeTwo, 5*time.Minute) &&
		waitReady(lb, 1*time.Minute)

	code := 1
	if ready {
		code = m.Run()
	} else {
		fmt.Println("cluster did not become ready")
	}

	if out, err := compose("down", "-v", "--remove-orphans"); err != nil {
		fmt.Printf("compose down failed: %v\n%s\n", err, out)
	}
	os.Exit(code)
}

// A message POSTed to node-one must converge to a fresh reader on node-two —
// over REAL JetStream, not InMemory. This is the reason the example exists.
func TestClusterConvergesAcrossNodesOverJetStream(t *testing.T) {
	// node-one and node-two render their own identity — two distinct processes.
	require.Equal(t, "node-one", bannerOf(t, getBody(t, http.DefaultClient, nodeOne)))
	require.Equal(t, "node-two", bannerOf(t, getBody(t, http.DefaultClient, nodeTwo)))

	marker := "e2e-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	require.NotContains(t, getBody(t, http.DefaultClient, nodeTwo), marker,
		"node-two must start without this run's marker")

	sendMessage(t, newJarClient(t), nodeOne, "alice", marker)

	require.Eventually(t, func() bool {
		return bytes.Contains([]byte(getBody(t, http.DefaultClient, nodeTwo)), []byte(marker))
	}, 30*time.Second, 500*time.Millisecond,
		"a message sent on node-one must converge to node-two via the JetStream backplane")
}

// The load balancer must (a) pin one browser to a single node across requests —
// otherwise that tab's SSE stream and action POSTs would land on different pods
// and break — and (b) still converge state for a different browser that may be
// pinned to the other node.
func TestStickyLBPinsAClientThenConvergesAcrossBrowsers(t *testing.T) {
	// One "browser": a single cookie jar. Repeated requests through the LB must
	// stay on the same node, because HAProxy's VIA_LB cookie pins it.
	browserA := newJarClient(t)
	first := bannerOf(t, getBody(t, browserA, lb))
	for i := 0; i < 6; i++ {
		require.Equal(t, first, bannerOf(t, getBody(t, browserA, lb)),
			"the LB must keep one browser pinned to the same node (sticky cookie)")
	}

	// browserA sends a message through the LB (its internal GET+POST share the
	// jar, so both hit browserA's pinned node).
	marker := "lb-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	sendMessage(t, browserA, lb, "alice", marker)

	// A different browser (fresh jar → possibly the other node) sees it via the
	// backplane, no matter which node the LB pinned it to.
	browserB := newJarClient(t)
	require.Eventually(t, func() bool {
		return bytes.Contains([]byte(getBody(t, browserB, lb)), []byte(marker))
	}, 30*time.Second, 500*time.Millisecond,
		"a message sent through the LB must converge to any other browser behind it")
}

// compose runs `docker compose -f docker-compose.yml <args...>` in the package dir.
func compose(args ...string) ([]byte, error) {
	full := append([]string{"compose", "-f", "docker-compose.yml"}, args...)
	return exec.Command("docker", full...).CombinedOutput()
}

// waitReady polls base until it serves the chat page or the deadline passes.
func waitReady(base string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && bytes.Contains(body, []byte("Via Cluster Chat")) {
				return true
			}
		}
		time.Sleep(time.Second)
	}
	return false
}

// newJarClient returns an http.Client with its own cookie jar — one "browser".
func newJarClient(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 5 * time.Second}
}

// getBody does a GET with the given client and returns the page HTML.
func getBody(t *testing.T, hc *http.Client, base string) string {
	t.Helper()
	resp, err := hc.Get(base + "/")
	if err != nil {
		t.Fatalf("GET %s: %v", base, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// bannerOf extracts the serving node name from a rendered page.
func bannerOf(t *testing.T, html string) string {
	t.Helper()
	m := bannerRE.FindStringSubmatch(html)
	if len(m) < 2 {
		t.Fatalf("no node banner in page")
	}
	return m[1]
}

// sendMessage replays Via's action protocol against base using hc's jar: GET to
// mint a tab id (and pick up the LB's sticky cookie), then POST /_action/Send
// carrying that tab id. GET and POST share the jar, so both reach the same node.
func sendMessage(t *testing.T, hc *http.Client, base, name, body string) {
	t.Helper()
	resp, err := hc.Get(base + "/")
	if err != nil {
		t.Fatalf("GET %s: %v", base, err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := tabRE.FindSubmatch(page)
	if len(m) < 2 {
		t.Fatalf("no via_tab id in %s page", base)
	}

	payload, _ := json.Marshal(map[string]any{
		"via_tab": string(m[1]),
		"name":    name,
		"draft":   body,
	})
	resp, err = hc.Post(base+"/_action/Send", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST Send to %s: %v", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST Send to %s: status %d", base, resp.StatusCode)
	}
}
