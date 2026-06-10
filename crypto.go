package via

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// ErrErased: an event's payload was encrypted under a per-data-subject key that
// has since been DROPPED (a GDPR erasure / crypto-shred). The plaintext is
// unrecoverable by design. The projector and consumers treat it like a poison
// record — skip it, advance past it — but emit via.events.erased instead of
// via.events.undecodable, because this is an intentional erasure, not corruption.
var ErrErased = errors.New("via: event encrypted under an erased data-subject key (crypto-shredded)")

// KeyStore is the per-data-subject key custodian behind crypto-shred GDPR
// erasure. Each data subject (a user, a tenant) gets its own symmetric key; an
// event that carries PII is encrypted under its subject's key before it is
// appended to the durable log. Erasure is DropKey: once a subject's key is gone,
// every ciphertext for that subject — in the append-only log, in backups, in a
// Kafka topic that keeps records forever — is permanently unreadable, without
// rewriting any history.
//
// Implementations must be safe for concurrent use. KeyFor is the create-or-get
// used at Append; Key is the lookup-only used at decode and MUST NOT recreate a
// dropped key (that would silently un-erase the subject); DropKey is idempotent.
type KeyStore interface {
	// KeyFor returns the subject's key, creating one if absent. Used at Append.
	KeyFor(ctx context.Context, subject string) ([]byte, error)
	// Key returns the subject's key and ok=true, or ok=false if it never existed
	// or was dropped. Used at decode — it MUST NOT create a key.
	Key(ctx context.Context, subject string) (key []byte, ok bool, err error)
	// DropKey permanently erases the subject's key (crypto-shred). Idempotent.
	DropKey(ctx context.Context, subject string) error
}

// DataSubject is implemented by an event type whose payload belongs to a single
// data subject. When a KeyStore is configured (WithKeyStore), such an event's
// payload is encrypted under that subject's key at Append, so erasing the
// subject (App.EraseDataSubject) crypto-shreds it from the durable log. An event
// that does not implement DataSubject (or returns "") is stored in plaintext.
type DataSubject interface {
	DataSubject() string
}

// InMemoryKeyStore returns a process-local KeyStore backed by a map. It is the
// reference implementation for tests and single-process apps; a clustered
// deployment supplies a shared KMS/Vault-backed KeyStore so every pod resolves
// and erases the same keys.
func InMemoryKeyStore() KeyStore { return &memKeyStore{keys: map[string][]byte{}} }

type memKeyStore struct {
	mu   sync.RWMutex
	keys map[string][]byte
}

func (m *memKeyStore) KeyFor(_ context.Context, subject string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if k, ok := m.keys[subject]; ok {
		return k, nil
	}
	k := make([]byte, 32) // AES-256
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("via: keystore generate key: %w", err)
	}
	m.keys[subject] = k
	return k, nil
}

func (m *memKeyStore) Key(_ context.Context, subject string) ([]byte, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	k, ok := m.keys[subject]
	return k, ok, nil
}

func (m *memKeyStore) DropKey(_ context.Context, subject string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, subject)
	return nil
}

// encryptPayload seals plaintext with AES-256-GCM under key and returns a JSON
// string token (base64 of nonce||ciphertext), so it rides the envelope's D field
// (a json.RawMessage) as ordinary JSON.
func encryptPayload(key, plaintext []byte) (json.RawMessage, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return json.Marshal(base64.StdEncoding.EncodeToString(sealed))
}

// decryptPayload reverses encryptPayload. A wrong key or tampered ciphertext
// yields an error (GCM authentication failure).
func decryptPayload(key []byte, token json.RawMessage) ([]byte, error) {
	var b64 string
	if err := json.Unmarshal(token, &b64); err != nil {
		return nil, err
	}
	sealed, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("via: ciphertext too short")
	}
	nonce, ct := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

// eventDecryptFn resolves an encrypted envelope payload to plaintext, or returns
// ErrErased when the subject's key is gone. nil when no KeyStore is configured.
type eventDecryptFn = func(subject string, token json.RawMessage) ([]byte, error)

// eventDecryptor returns the decode-time payload decryptor bound to this App's
// KeyStore, or nil if none is configured (encrypted records then fail to decode
// → ErrUndecodable, never silently mis-folded).
func (a *App) eventDecryptor() eventDecryptFn {
	ks := a.cfg.keyStore
	if ks == nil {
		return nil
	}
	return func(subject string, token json.RawMessage) ([]byte, error) {
		key, ok, err := ks.Key(a.backplaneCtx, subject)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrErased // key dropped → crypto-shredded
		}
		return decryptPayload(key, token)
	}
}

// erasureGenKey is the Store cell holding the global crypto-shred generation: a
// monotonically increasing counter bumped on every EraseDataSubject, so cold
// starts can invalidate snapshots folded before an erasure.
const erasureGenKey = "erasure:gen"

// loadErasureGen reads the authoritative crypto-shred generation from the Store
// (0 if never set). Read at cold start (to invalidate stale snapshots) and at
// snapshot write (to stamp the checkpoint).
func (a *App) loadErasureGen() uint64 {
	data, _, ok, err := a.backplane.LoadSnapshot(a.backplaneCtx, erasureGenKey)
	if err != nil || !ok {
		return 0
	}
	var gen uint64
	if json.Unmarshal(data, &gen) != nil {
		return 0
	}
	return gen
}

// EraseDataSubject performs a GDPR crypto-shred: it drops the subject's key (so
// every ciphertext for them in the durable log becomes permanently unreadable)
// and bumps the global erasure generation (so any pod cold-starting from the log
// re-folds without the now-undecryptable events instead of seeding a pre-erasure
// snapshot that still holds their plaintext PII).
//
// What is shredded, precisely:
//   - The LOG is cryptographically shredded: every record for the subject is
//     ciphertext under the dropped key, unrecoverable forever (the strong
//     guarantee — survives append-only logs and backups).
//   - SNAPSHOTS are invalidated by generation: a pre-erasure snapshot (whose
//     folded V may hold the subject's plaintext) is never seeded into a
//     projection again, and is physically overwritten by post-erasure V on the
//     key's next snapshot write. The plaintext may linger in the snapshot CELL
//     until that next write, so a deployment with strict erasure SLAs must also
//     expire the snapshot store per its retention policy.
//
// Not covered in v1: a pod's already-running in-memory projection still holds the
// pre-erasure value until it cold-starts / redeploys (it re-folds clean then). A
// key that has been COMPACTED (its event prefix discarded) cannot be re-folded
// after erasure — its snapshot is durable genesis — so compaction + erasure of
// the same key needs snapshot re-encryption, a documented follow-up.
func (a *App) EraseDataSubject(ctx context.Context, subject string) error {
	if a.cfg.keyStore == nil {
		return errors.New("via: EraseDataSubject requires WithKeyStore")
	}
	if err := a.cfg.keyStore.DropKey(ctx, subject); err != nil {
		return fmt.Errorf("via: drop key for %q: %w", subject, err)
	}
	// Bump the global generation (CAS loop) to invalidate pre-erasure snapshots.
	for {
		data, rev, ok, err := a.backplane.LoadSnapshot(ctx, erasureGenKey)
		if err != nil {
			return fmt.Errorf("via: load erasure gen: %w", err)
		}
		var gen uint64
		if ok {
			_ = json.Unmarshal(data, &gen)
		}
		next, err := json.Marshal(gen + 1)
		if err != nil {
			return err
		}
		_, err = a.backplane.CAS(ctx, erasureGenKey, rev, next)
		if errors.Is(err, ErrCASConflict) {
			continue // a concurrent erasure moved it; reload and retry
		}
		if err != nil {
			return fmt.Errorf("via: bump erasure gen: %w", err)
		}
		return nil
	}
}
