package via

import (
	"context"
	"encoding/json"
)

// applyRecord handles one delivered record. It first guards against COMPACTION
// OVERTAKE: if this projector has folded up to cursor C but the next record's
// offset is beyond C+1, the log dropped records C+1..(offset-1) that this pod
// never folded (a faster peer compacted the shared log below this pod's
// position). Folding the post-gap record onto the stale projection would
// silently and permanently diverge this pod from its peers — the cross-pod
// truncation bug. Instead re-seed from the durable snapshot (which covers the
// gap) and let the fold continue; if no snapshot can recover the gap, HALT
// rather than diverge. Then delegate to projectRecord. Runs on the single
// projector goroutine, so the read-then-reseed is not racing another folder.
func (a *App) applyRecord(ls *logState, key string, rec Record) bool {
	ls.mu.RLock()
	cur, halted, gapsBenign := ls.cursor, ls.halted, ls.gapsBenign
	ls.mu.RUnlock()
	// rec.Offset > cur+1 is a gap: offsets cur+1..rec.Offset-1 were not delivered.
	// That can mean a compacted prefix (real data loss) OR simply a backend whose
	// per-key offsets are not contiguous — e.g. a JetStream stream sequenced
	// GLOBALLY across subjects, where each key's records carry the shared stream
	// sequence and skip the numbers other keys took. classifyGap tells them apart;
	// once a backend proves non-contiguous we latch (gapsBenign) and stop checking,
	// so a busy shared stream does not pay a snapshot read per record.
	if !halted && !gapsBenign && rec.Offset > cur+1 {
		switch a.classifyGap(ls, key, cur, rec.Offset-1) {
		case gapReseeded:
			a.metricsOrNoop().Counter("via.events.compaction_reseed", "key", key)
		case gapHalt:
			ls.mu.Lock()
			ls.halted = true
			ls.mu.Unlock()
			a.metricsOrNoop().Counter("via.events.compaction_gap_halt", "key", key)
			return false
		case gapBenign:
			// Not a lost prefix — fold the record and latch so subsequent
			// non-contiguous records skip the gap check (and its snapshot read).
			ls.mu.Lock()
			ls.gapsBenign = true
			ls.mu.Unlock()
		}
	}
	return a.projectRecord(ls, key, rec)
}

// gapClass is how applyRecord interprets an offset gap.
type gapClass int

const (
	gapBenign   gapClass = iota // not a lost prefix → fold the record
	gapReseeded                 // recovered from a bridging snapshot
	gapHalt                     // a compacted prefix we cannot bridge → freeze (never truncate)
)

// classifyGap decides what a gap (rec.Offset > cursor+1) means. A gap is a lost
// prefix ONLY if compaction discarded it, and compaction always writes a
// Compacted snapshot FIRST (snapshot-FIRST). So:
//   - no snapshot, or an unreadable one          → benign (no compaction ran)
//   - a snapshot that bridges the gap            → reseed from it (projection=V,
//     cursor=CoveredOffset), recovering the dropped prefix instead of skipping it
//   - a Compacted snapshot that cannot bridge    → halt, never truncate to the tail
//   - an uncompacted snapshot that cannot bridge → benign (it is a disposable
//     cache; the gap is just non-contiguous offsets, nothing was lost)
//
// The backplane read is OUTSIDE ls.mu; seedFromSnapshot takes the lock.
func (a *App) classifyGap(ls *logState, key string, cur, needCovered Offset) gapClass {
	data, rev, ok, _ := a.backplane.LoadSnapshot(context.Background(), snapKey(key))
	if !ok {
		return gapBenign
	}
	var cp checkpoint
	if json.Unmarshal(data, &cp) != nil {
		return gapBenign
	}
	// Recover only if the snapshot BRIDGES the gap (covers up to the record before
	// rec) and moves us FORWARD — never seed behind our current position.
	if cp.CodecHash == ls.codecHash && cp.CoveredOffset >= needCovered && cp.CoveredOffset > cur {
		if v, err := ls.decodeSnap(cp.V); err == nil {
			a.seedFromSnapshot(ls, v, cp, rev)
			return gapReseeded
		}
	}
	if cp.Compacted {
		return gapHalt
	}
	return gapBenign
}
