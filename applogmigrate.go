package via

import "sync"

// snapMigrations bridges a COMPACTED key's durable-genesis snapshot across a
// change to the projected type V, keyed by the PREVIOUS V codec hash. Stored
// type-erased (the App is type-erased); the typed RegisterSnapshotMigration
// captures V in the closure.
var (
	snapMigrationsMu sync.RWMutex
	snapMigrations   = map[string]func([]byte) (any, error){}
)

// RegisterSnapshotMigration teaches the runtime how to bridge a COMPACTED key's
// durable-genesis snapshot across a change to the projected type V.
//
// Once a key has compacted, the discarded event prefix is unrecoverable, so a
// snapshot codec-hash mismatch can no longer be resolved by re-folding from
// genesis — the snapshot IS the genesis. Register, keyed by the PREVIOUS V codec
// hash (reflect.TypeFor[OldV]().String()), a function that decodes the old
// snapshot bytes into the current V; on cold start the runtime seeds from it and
// folds the retained tail on top. A compacted key whose old hash has no
// registered migration (or whose migration errors) HALTS its projector
// (roll-forward-only) rather than silently truncate to the surviving tail.
//
// Uncompacted keys never consult the registry — their snapshot stays a pure
// disposable cache (mismatch → re-fold from genesis), so evolving V is free in
// the common case. Register at init, before the App mounts.
func RegisterSnapshotMigration[V any](fromCodecHash string, migrate func([]byte) (V, error)) {
	snapMigrationsMu.Lock()
	defer snapMigrationsMu.Unlock()
	snapMigrations[fromCodecHash] = func(b []byte) (any, error) {
		v, err := migrate(b)
		if err != nil {
			return nil, err
		}
		return v, nil
	}
}

// lookupSnapMigration returns the migration registered for an old codec hash.
func lookupSnapMigration(fromCodecHash string) (func([]byte) (any, error), bool) {
	snapMigrationsMu.RLock()
	defer snapMigrationsMu.RUnlock()
	fn, ok := snapMigrations[fromCodecHash]
	return fn, ok
}

// deleteSnapMigration removes a registration — used by internal tests to keep
// the process-global registry clean across cases.
func deleteSnapMigration(fromCodecHash string) {
	snapMigrationsMu.Lock()
	defer snapMigrationsMu.Unlock()
	delete(snapMigrations, fromCodecHash)
}
