package via

import "sync"

// snapshotMigration is the type-erased boundary for a registered snapshot
// migration: it decodes a COMPACTED key's old durable-genesis snapshot bytes
// into the current projected type V (returned as any, since the App is
// type-erased). Naming the boundary confines the one unavoidable
// func([]byte)(any,error) erasure to a single type, instead of repeating it
// across the registry, the lookup signature, and the call site.
type snapshotMigration struct {
	// decode turns old-codec snapshot bytes into the current V (as any). The
	// typed RegisterSnapshotMigration captures the concrete V in this closure.
	decode func([]byte) (any, error)
}

// snapMigrations bridges a COMPACTED key's durable-genesis snapshot across a
// change to the projected type V, keyed by the PREVIOUS V codec hash.
var (
	snapMigrationsMu sync.RWMutex
	snapMigrations   = map[string]snapshotMigration{}
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
	snapMigrations[fromCodecHash] = snapshotMigration{
		decode: func(b []byte) (any, error) {
			v, err := migrate(b)
			if err != nil {
				return nil, err
			}
			return v, nil
		},
	}
}

// lookupSnapMigration returns the migration registered for an old codec hash.
func lookupSnapMigration(fromCodecHash string) (snapshotMigration, bool) {
	snapMigrationsMu.RLock()
	defer snapMigrationsMu.RUnlock()
	m, ok := snapMigrations[fromCodecHash]
	return m, ok
}

// deleteSnapMigration removes a registration — used by internal tests to keep
// the process-global registry clean across cases.
func deleteSnapMigration(fromCodecHash string) {
	snapMigrationsMu.Lock()
	defer snapMigrationsMu.Unlock()
	delete(snapMigrations, fromCodecHash)
}
