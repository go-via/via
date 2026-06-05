package via

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// pii is an event carrying a single data subject's personal data. It opts into
// per-subject encryption by implementing DataSubject.
type pii struct{ User, Secret string }

func (pii) Fold(acc []string, e pii) []string {
	return append(append([]string(nil), acc...), e.User+":"+e.Secret)
}
func (p pii) DataSubject() string { return p.User }

func bindPII(app *App, key string) {
	var h StateAppEvents[pii, []string]
	h.bindWireKey(key)
	h.bindApp(app)
}

func projStrings(app *App, key string) []string {
	v, ok := app.logProjection(key)
	if !ok {
		return nil
	}
	s, _ := v.([]string)
	return s
}

// The whole point of crypto-shred: a data subject's PII must be UNRECOVERABLE
// from the durable log after erasure, without rewriting history. With a KeyStore
// configured, a DataSubject event's payload is encrypted at rest (the secret is
// never in the raw record), and EraseDataSubject drops the key so the ciphertext
// can never again be decoded — a fresh pod cold-starting from the same log folds
// the erased subject's events to nothing, while every other subject survives.
func TestErasedSubjectIsCryptoShreddedFromTheLog(t *testing.T) {
	t.Parallel()
	ks := InMemoryKeyStore()
	bp := InMemory()
	defer bp.Close()
	var server *httptest.Server
	// No snapshot interval: this test asserts crypto-shred from the LOG itself, so
	// the log must stay intact (snapshotting at interval 1 would also COMPACT it,
	// discarding alice's first record by truncation rather than by erasure — the
	// compaction+erasure interaction is a separate, documented v1 follow-up).
	app := New(WithTestServer(&server), WithBackplane(bp), WithKeyStore(ks))
	defer server.Close()
	bindPII(app, "k")
	ctx := context.Background()

	for _, e := range []pii{{"alice", "alice-secret"}, {"bob", "bob-secret"}, {"alice", "alice-again"}} {
		appendEvent(t, app, "k", e)
	}
	require.Eventually(t, func() bool { return len(projStrings(app, "k")) == 3 },
		2*time.Second, 10*time.Millisecond, "all three events fold")

	// Encryption at rest: the raw log records must NOT contain alice's plaintext
	// secret — it is ciphertext on the wire.
	for _, raw := range drainLog(t, bp, "k") {
		require.False(t, bytes.Contains(raw, []byte("alice-secret")),
			"PII must be encrypted at rest, found plaintext in a record")
	}

	// Erase alice (crypto-shred).
	require.NoError(t, app.EraseDataSubject(ctx, "alice"))

	// A fresh pod cold-starting from the SAME log + keystore must fold to bob's
	// data only — alice's events are now undecryptable and dropped.
	spy := &spyMetrics{}
	var server2 *httptest.Server
	app2 := New(WithTestServer(&server2), WithBackplane(bp), WithKeyStore(ks), WithMetrics(spy))
	defer server2.Close()
	bindPII(app2, "k")

	// With the full log intact, the fresh pod re-folds all three records: both of
	// alice's are now undecryptable (skipped, metered erased), bob's survives. Wait
	// on the erased metric too — the surviving projection ([bob]) can be reached
	// after folding bob's record but BEFORE alice's trailing erased record is
	// processed, so asserting only the projection would race the metric.
	require.Eventually(t, func() bool {
		got := projStrings(app2, "k")
		return len(got) == 1 && got[0] == "bob:bob-secret" && spy.saw("via.events.erased")
	}, 2*time.Second, 10*time.Millisecond,
		"after erasure a fresh pod folds only the surviving subject and meters the erased records")
}

// Without a KeyStore, an encrypted record cannot be decoded — it must be DROPPED
// (ErrUndecodable → skip+advance), never silently mis-folded as if plaintext. A
// pod that lost its KeyStore config must fail safe, not leak garbage.
func TestEncryptedRecordWithoutKeyStoreIsDropped(t *testing.T) {
	t.Parallel()
	ks := InMemoryKeyStore()
	bp := InMemory()
	defer bp.Close()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(bp), WithKeyStore(ks))
	defer server.Close()
	bindPII(app, "k")
	appendEvent(t, app, "k", pii{"alice", "s"})

	spy := &spyMetrics{}
	var server2 *httptest.Server
	app2 := New(WithTestServer(&server2), WithBackplane(bp), WithMetrics(spy)) // no keystore
	defer server2.Close()
	bindPII(app2, "k")

	require.Eventually(t, func() bool { return spy.saw("via.events.undecodable") },
		2*time.Second, 10*time.Millisecond, "an encrypted record is undecodable without a keystore")
	require.Empty(t, projStrings(app2, "k"), "an undecodable encrypted record must not fold")
}

// An event that does NOT implement DataSubject is stored in plaintext even when a
// KeyStore is configured — encryption is opt-in per event type, so non-PII
// events pay nothing and stay readable by a pod without the key.
func TestNonDataSubjectEventsStayPlaintext(t *testing.T) {
	t.Parallel()
	ks := InMemoryKeyStore()
	bp := InMemory()
	defer bp.Close()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(bp), WithKeyStore(ks))
	defer server.Close()
	bindLog(app, "k") // envEv does not implement DataSubject
	if _, err := app.backplane.Append(context.Background(), "k", goodEnv(t, envEv{N: 42})); err != nil {
		t.Fatalf("append: %v", err)
	}
	require.Eventually(t, func() bool {
		p := projection(app, "k")
		return len(p) == 1 && p[0] == 42
	}, 2*time.Second, 10*time.Millisecond, "a non-DataSubject event folds normally under a keystore")
}

// appendEvent marshals ev through the REAL Append codec (envelope + per-subject
// encryption) and commits it, so tests exercise encryption without fabricating a
// via *Ctx.
func appendEvent[E any](t *testing.T, app *App, key string, ev E) {
	t.Helper()
	data, err := marshalEvent(app, ev)
	require.NoError(t, err)
	_, err = app.backplane.Append(context.Background(), key, data)
	require.NoError(t, err)
}

// A side-effect consumer that tails an erased subject's old event (encrypted
// under a now-dropped key) must SKIP it (the effect can never run on shredded
// data) and advance past it — not block forever — recording via.consumer.erased,
// distinct from undecodable corruption. A fresh consumer cold-starting after the
// erasure exercises exactly this re-delivery path.
func TestConsumerSkipsErasedEvent(t *testing.T) {
	t.Parallel()
	ks := InMemoryKeyStore()
	bp := InMemory()
	defer bp.Close()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(bp), WithKeyStore(ks))
	defer server.Close()
	ctx := context.Background()

	bindPII(app, "k")
	appendEvent(t, app, "k", pii{"alice", "s1"})
	appendEvent(t, app, "k", pii{"bob", "s2"})

	// Erase alice — her record's key is gone.
	require.NoError(t, app.EraseDataSubject(ctx, "alice"))

	// A fresh consumer tailing from genesis re-delivers both records: alice's is
	// now undecryptable (skipped, metered), bob's still fires.
	spy := &spyMetrics{}
	var server2 *httptest.Server
	app2 := New(WithTestServer(&server2), WithBackplane(bp), WithKeyStore(ks), WithMetrics(spy))
	defer server2.Close()
	var hdl StateAppEvents[pii, []string]
	hdl.bindWireKey("k")
	hdl.bindApp(app2)

	var mu sync.Mutex
	var fired []string
	hdl.OnEvent("sink", func(_ context.Context, e pii, _ Offset) error {
		mu.Lock()
		fired = append(fired, e.User)
		mu.Unlock()
		return nil
	})

	require.Eventually(t, func() bool { return spy.saw("via.consumer.erased") },
		2*time.Second, 10*time.Millisecond, "an erased record must emit via.consumer.erased and be skipped")
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(fired) == 1 && fired[0] == "bob"
	}, 2*time.Second, 10*time.Millisecond, "the surviving subject's event still fires")
}

// AES-256-GCM must AUTHENTICATE: decrypting a ciphertext under the WRONG key (or
// any tampered byte) must ERROR via the GCM auth tag, never silently return
// garbage plaintext. A silent wrong-key decode would mis-fold one subject's data
// as another's — the exact failure crypto-shred exists to prevent.
func TestDecryptPayloadWrongKeyFailsAuthentication(t *testing.T) {
	t.Parallel()
	key := make([]byte, 32)
	key[0] = 1
	token, err := encryptPayload(key, []byte(`{"User":"alice"}`))
	require.NoError(t, err)

	wrong := make([]byte, 32)
	wrong[0] = 2
	_, err = decryptPayload(wrong, token)
	require.Error(t, err, "a wrong key must fail GCM authentication, not return garbage")

	// A tampered ciphertext under the RIGHT key must also fail the auth tag.
	var b64 string
	require.NoError(t, json.Unmarshal(token, &b64))
	sealed, derr := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, derr)
	sealed[len(sealed)-1] ^= 0xFF // flip a tag byte
	tampered, merr := json.Marshal(base64.StdEncoding.EncodeToString(sealed))
	require.NoError(t, merr)
	_, err = decryptPayload(key, json.RawMessage(tampered))
	require.Error(t, err, "a tampered ciphertext must fail GCM authentication")
}

// A short AES key is a configuration error and must fail loudly at encrypt time,
// never silently produce a weak/garbled record.
func TestEncryptPayloadRejectsBadKeyLength(t *testing.T) {
	t.Parallel()
	_, err := encryptPayload(make([]byte, 16+1), []byte("x")) // 17 bytes: not a valid AES key
	require.Error(t, err, "a non-AES key length must error")
}

// The round trip must be lossless AND every Seal must use a FRESH nonce: two
// encryptions of the SAME plaintext under the SAME key must differ (no nonce
// reuse), or AES-GCM's security collapses.
func TestEncryptPayloadFreshNoncePerSeal(t *testing.T) {
	t.Parallel()
	key := make([]byte, 32)
	pt := []byte(`{"User":"alice","Secret":"s"}`)
	a, err := encryptPayload(key, pt)
	require.NoError(t, err)
	b, err := encryptPayload(key, pt)
	require.NoError(t, err)
	require.NotEqual(t, string(a), string(b), "each Seal must use a fresh nonce; identical ciphertexts mean nonce reuse")
	out, err := decryptPayload(key, a)
	require.NoError(t, err)
	require.Equal(t, pt, out, "round trip must be lossless")
}

// EraseDataSubject without a configured KeyStore is a usage error and must return
// an error (not panic, not silently no-op leaving the subject un-erased).
func TestEraseDataSubjectRequiresKeyStore(t *testing.T) {
	t.Parallel()
	bp := InMemory()
	defer bp.Close()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(bp)) // no keystore
	defer server.Close()
	require.Error(t, app.EraseDataSubject(context.Background(), "alice"),
		"EraseDataSubject without WithKeyStore must error")
}

// The erasure generation is durable and monotonic: each EraseDataSubject bumps it
// by one, and loadErasureGen reads back the authoritative value. Two erasures of
// different subjects must leave gen==2 — the CAS-bump path (incl. the reload leg)
// is exercised, and a fresh cold start would invalidate any snapshot below it.
func TestEraseDataSubjectBumpsDurableGeneration(t *testing.T) {
	t.Parallel()
	ks := InMemoryKeyStore()
	bp := InMemory()
	defer bp.Close()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(bp), WithKeyStore(ks))
	defer server.Close()
	ctx := context.Background()

	require.Equal(t, uint64(0), app.loadErasureGen(), "no erasure yet → gen 0")
	require.NoError(t, app.EraseDataSubject(ctx, "alice"))
	require.Equal(t, uint64(1), app.loadErasureGen(), "first erasure → gen 1")
	require.NoError(t, app.EraseDataSubject(ctx, "bob"))
	require.Equal(t, uint64(2), app.loadErasureGen(), "second erasure → gen 2")
}

// Concurrent erasures must all land: the CAS-bump loop reloads-and-retries on a
// peer's conflicting bump (the ErrCASConflict continue leg), so N concurrent
// EraseDataSubject calls leave the durable generation at exactly N — none lost.
func TestEraseDataSubjectConcurrentBumpsAllLand(t *testing.T) {
	t.Parallel()
	ks := InMemoryKeyStore()
	bp := InMemory()
	defer bp.Close()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(bp), WithKeyStore(ks))
	defer server.Close()

	const n = 16
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			require.NoError(t, app.EraseDataSubject(context.Background(), "subject-"+string(rune('a'+i))))
		}(i)
	}
	wg.Wait()
	require.Equal(t, uint64(n), app.loadErasureGen(),
		"every concurrent erasure must bump the generation (CAS retry loses none)")
}

// A COMPACTED snapshot (durable genesis — its event prefix is gone) that an
// erasure has invalidated must NOT be re-folded from offset 0: the prefix is
// unavailable, so re-folding would silently TRUNCATE to the surviving tail,
// dropping data belonging to SURVIVING subjects, not just the erased one. The
// projector must HALT (roll-forward-only) instead, surfacing the unsupported
// compaction+erasure case rather than corrupting other subjects' state.
func TestErasureInvalidatedCompactedSnapshotHaltsInsteadOfTruncating(t *testing.T) {
	t.Parallel()
	bp := InMemory()
	defer bp.Close()
	ctx := context.Background()

	// Plant a COMPACTED checkpoint at an OLD erasure generation, then bump the
	// authoritative generation past it.
	cp := checkpoint{Epoch: 0, CoveredOffset: 5, CodecHash: "stale", V: mustJSON([]int{10, 20}), Compacted: true, ErasureGen: 0}
	if _, err := bp.CAS(ctx, snapKey("k"), 0, mustJSON(cp)); err != nil {
		t.Fatalf("plant checkpoint: %v", err)
	}
	if _, err := bp.CAS(ctx, erasureGenKey, 0, mustJSON(uint64(1))); err != nil {
		t.Fatalf("bump gen: %v", err)
	}

	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithBackplane(bp), WithMetrics(spy))
	defer server.Close()
	bindLog(app, "k")

	// The projector must HALT: it neither seeds the stale snapshot ([10,20]) nor
	// truncates to a re-fold — and a later append is NOT folded (frozen).
	require.Eventually(t, func() bool { return spy.saw("via.snapshot.erasure_halt") },
		2*time.Second, 10*time.Millisecond, "a compacted+erased snapshot must halt the projector")
	_, _ = app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: 99}))
	require.Never(t, func() bool { return len(projection(app, "k")) > 0 },
		400*time.Millisecond, 50*time.Millisecond,
		"a halted projector must not fold (no truncation, no seed of stale PII)")
}

func drainLog(t *testing.T, bp Backplane, key string) [][]byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ch, err := bp.Subscribe(ctx, key, 0)
	require.NoError(t, err)
	var out [][]byte
	for {
		select {
		case r, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, append([]byte(nil), r.Data...))
			if len(out) >= 3 {
				return out
			}
		case <-ctx.Done():
			return out
		}
	}
}
