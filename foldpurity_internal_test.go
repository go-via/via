package via

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// goodEnvBytes builds a current-version envelope for envEv{N:n} without a
// *testing.T, so it can seed the fuzz corpus and the cross-process replay.
func goodEnvBytes(n int) []byte {
	d, _ := json.Marshal(envEv{N: n})
	b, err := json.Marshal(eventEnvelope{T: "envEv", V: currentEventVersion, D: d})
	if err != nil {
		panic(err)
	}
	return b
}

// Fold determinism is the #1 correctness risk of the event-log model: if a
// reducer is impure (reads time, a package global, a map iteration order, an
// RNG) two pods replaying the same log diverge, and a snapshot crystallizes the
// divergence permanently. WithFoldVerify is same-process and can't catch a
// reducer that reads cross-process state. These two tests are the gates the
// council mandated (T1-TEST-1): a fuzz that folding the SAME bytes is
// deterministic + never panics, and a SUBPROCESS replay that two independent
// processes fold a fixed log to the identical digest.

// FuzzFoldIsDeterministicAndNeverPanics drives arbitrary bytes through the real
// projector decode+fold path. The decode must classify every input (fold,
// ErrUndecodable, or ErrForwardIncompatible) WITHOUT panicking — a poison record
// must never crash a pod — and folding the same bytes from the same accumulator
// twice must yield the identical result and error (purity). A reducer that read
// a clock or RNG would fail the second-fold equality.
func FuzzFoldIsDeterministicAndNeverPanics(f *testing.F) {
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()
	fold := bindLog(app, "k")

	// Seed with well-formed envelopes and assorted poison.
	for _, n := range []int{0, 1, 7, -3, 1000} {
		f.Add(goodEnvBytes(n))
	}
	f.Add([]byte("garbage"))
	f.Add([]byte(`{"t":"envEv","v":99,"d":{"N":1}}`)) // forward-incompatible
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		acc := []int{1, 2, 3}
		r1, e1 := fold(append([]int(nil), acc...), data)
		r2, e2 := fold(append([]int(nil), acc...), data)

		// Same input → same error classification.
		require.Equal(t, e1 == nil, e2 == nil, "fold error determinism for %q", data)
		if e1 != nil {
			require.Equal(t, e1.Error(), e2.Error(), "fold error must be deterministic for %q", data)
			return
		}
		// Same input + same acc → identical projection (purity).
		got1, _ := r1.([]int)
		got2, _ := r2.([]int)
		require.Equal(t, got1, got2, "fold must be deterministic for %q", data)
	})
}

// TestFoldConvergesAcrossProcesses replays a fixed event log in two SEPARATE OS
// processes and asserts both reach the identical projection digest. Same-process
// determinism (the fuzz above) cannot catch a reducer that reads process-global
// state (a package var, an env-seeded value) — two goroutines share it, two
// processes do not. This is the only gate that distinguishes "pure" from "agrees
// with itself in one process".
func TestFoldConvergesAcrossProcesses(t *testing.T) {
	t.Parallel()
	if os.Getenv("VIA_FOLD_REPLAY_CHILD") == "1" {
		fmt.Printf("VIA_FOLD_DIGEST=%d\n", replayFixedLogDigest())
		return
	}
	d1 := runReplayChild(t)
	d2 := runReplayChild(t)
	require.NotZero(t, d1, "child must produce a digest")
	require.Equal(t, d1, d2, "two independent processes must fold the same log to the same digest")
}

// replayFixedLogDigest folds a fixed, deterministic event sequence through the
// real projector decode+fold path and returns an fnv digest of the projection —
// the value an independent pod must reproduce exactly.
func replayFixedLogDigest() uint32 {
	var server *httptest.Server
	app := New(WithTestServer(&server))
	defer server.Close()
	fold := bindLog(app, "k")

	var acc any = []int(nil)
	for n := range 100 {
		next, err := fold(acc, goodEnvBytes(n*7-13))
		if err != nil {
			panic(err) // the fixed corpus is all well-formed; an error is a regression
		}
		acc = next
	}
	got, _ := acc.([]int)
	h := fnv.New32a()
	for _, v := range got {
		_, _ = fmt.Fprintf(h, "%d,", v)
	}
	return h.Sum32()
}

// runReplayChild re-execs this test binary, running only this test with the
// child env set, and parses the digest line the child prints.
func runReplayChild(t *testing.T) uint32 {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestFoldConvergesAcrossProcesses$", "-test.v")
	cmd.Env = append(os.Environ(), "VIA_FOLD_REPLAY_CHILD=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "child process failed: %s", out)
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "VIA_FOLD_DIGEST="); ok {
			var d uint32
			_, err := fmt.Sscanf(v, "%d", &d)
			require.NoError(t, err)
			return d
		}
	}
	t.Fatalf("child did not print a digest:\n%s", out)
	return 0
}
