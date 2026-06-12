package via

import (
	"context"
	"encoding/json"
)

// broadcastKey is the shared EventLog feed carrying broadcast payloads across
// pods. Unlike the value-less change feed, a broadcast record IS the payload —
// there is no Store cell behind it, because a broadcast is ephemeral and
// best-effort (no convergence, no reconcile, no monotone gate).
const broadcastKey = "via.broadcast"

const (
	bcScript  = "script"
	bcSignals = "signals"
)

// broadcastRecord is one cross-pod broadcast, carried whole on the feed.
type broadcastRecord struct {
	Kind    string         `json:"kind"`
	Script  string         `json:"script,omitempty"`
	Signals map[string]any `json:"signals,omitempty"`
}

// Broadcast queues a JavaScript snippet on every currently-live tab's
// patch queue. The next SSE drain on each tab pushes it to the browser.
// Useful for "page will reload in 30 seconds" maintenance notices,
// site-wide flash messages, or coordinated state invalidation.
//
//	app.Broadcast(`alert("Maintenance in 30 seconds.")`)
//	time.Sleep(30 * time.Second)
//	app.Shutdown(ctx)
//
// When a backplane is wired (via [WithBackplane]) the script also rides the
// shared feed and reaches every live tab on every pod; otherwise it stays
// pod-local. Either way the returned count is THIS pod's live-tab count at call
// time — the cluster-wide total is unknowable synchronously. Empty script is a
// no-op.
//
// EXPERIMENTAL: the single-process behavior is stable; the cross-pod semantics
// (the BroadcastNotify / BroadcastSignals family included) ride the pre-GA
// backplane and may change before 1.0.
func (a *App) Broadcast(script string) int {
	if script == "" {
		return 0
	}
	return a.dispatchBroadcast(broadcastRecord{Kind: bcScript, Script: script})
}

// BroadcastNotify shows an XSS-safe notification on every currently-live tab —
// the safe form of [App.Broadcast] for the common site-wide-notice case, so
// callers never hand-build (and mis-escape) the notification JS. message is
// JSON-encoded, so arbitrary text including markup is inert. Like Broadcast it
// reaches every pod when a backplane is wired and stays pod-local otherwise;
// the returned count is this pod's live-tab count. Empty message is a no-op.
func (a *App) BroadcastNotify(message string) int {
	if message == "" {
		return 0
	}
	script, ok := buildToastScript(message)
	if !ok {
		return 0
	}
	return a.Broadcast(script)
}

// BroadcastSignal pushes one typed signal value to every currently-live
// tab via its Signal[T] handle — the typed counterpart of
// [App.BroadcastSignals] for signals bound at Mount. Returns the tab
// count; nil sig is a no-op.
func BroadcastSignal[T any](a *App, sig *Signal[T], value T) int {
	if a == nil || sig == nil {
		return 0
	}
	return a.BroadcastSignals(map[string]any{sig.Key(): value})
}

// BroadcastSignals pushes a signal patch to every currently-live tab.
// Useful for site-wide announcements that drive a banner via a
// client-only signal (e.g. "$_systemNotice = 'planned maintenance'")
// without rendering each composition.
//
// Like [App.Broadcast], the patch rides the shared feed to every pod when a
// backplane is wired and stays pod-local otherwise; the returned count is this
// pod's live-tab count at call time.
//
// This is the untyped escape hatch for dynamic / client-only signal
// keys; when a *Signal[T] handle exists, prefer [BroadcastSignal].
func (a *App) BroadcastSignals(values map[string]any) int {
	if len(values) == 0 {
		return 0
	}
	return a.dispatchBroadcast(broadcastRecord{Kind: bcSignals, Signals: values})
}

// dispatchBroadcast routes one record: when clustered it Appends to the shared
// feed and lets the tailer apply on EVERY pod (including this one — append-only,
// never also applied directly, so the originating pod sees it exactly once);
// otherwise it applies locally in-process. Returns this pod's live-tab count.
func (a *App) dispatchBroadcast(rec broadcastRecord) int {
	if a.cfg.backplane != nil {
		if b, err := json.Marshal(rec); err == nil {
			if _, err := a.backplane.Append(a.backplaneCtx, broadcastKey, b); err != nil {
				a.logWarn(nil, "via: backplane Append failed dispatching broadcast: %v", err)
			}
		}
		return len(a.snapshotContexts())
	}
	return a.applyBroadcast(rec)
}

// applyBroadcast pushes one record onto every live tab's patch queue on THIS
// pod. It is the single apply path — used directly in single-pod mode and by
// the cross-pod tailer — so the two never diverge. Returns the tab count.
func (a *App) applyBroadcast(rec broadcastRecord) int {
	ctxs := a.snapshotContexts()
	switch rec.Kind {
	case bcScript:
		for _, c := range ctxs {
			enqueueScript(c, rec.Script)
		}
	case bcSignals:
		for _, c := range ctxs {
			c.patch.Signals(rec.Signals)
		}
	}
	return len(ctxs)
}

// startBroadcastTailer tails the shared broadcast feed and applies each record
// to this pod's live tabs. Runs on tailLoop, so a boot-time Head/Subscribe
// failure is retried rather than fatal and a transient drop re-subscribes.
// Every (re)connect re-Heads: broadcast is ephemeral, not convergent, so a
// pod never replays notices issued before it booted, and frames missed during
// a reconnect gap are skipped — resuming from the current head is correct.
// Only started when clustered.
func (a *App) startBroadcastTailer() {
	a.startTailer(tailer{
		feed: "broadcast",
		key:  broadcastKey,
		resumeFrom: func(ctx context.Context) (Offset, error) {
			head, _, err := a.backplane.Head(ctx, broadcastKey)
			return head, err
		},
		onRecord: func(rec Record) {
			var r broadcastRecord
			if json.Unmarshal(rec.Data, &r) != nil {
				return
			}
			a.applyBroadcast(r)
		},
	})
}

// broadcastRender forces a view re-render on every live *Ctx whose
// most recent render read key, except the writer (skipping it avoids
// re-entering its action mutex). When sess is non-nil only ctxs on
// that session are included — the scope for session-scoped writes
// that must not wake unrelated sessions. The writer's own re-render
// happens through the action's autoflush.
func (a *App) broadcastRender(skip *Ctx, sess *session, key string) {
	if skip != nil && skip.silent.Load() {
		return
	}
	for _, c := range a.snapshotContexts() {
		if c == skip {
			continue
		}
		if sess != nil && c.session.Load() != sess {
			continue
		}
		if !c.subscribed(key) {
			continue
		}
		go c.SyncNow()
	}
}

// snapshotContexts copies every live *Ctx into a slice under the
// registry RLock, so callers can iterate without holding the lock —
// the per-Ctx work (enqueueScript, Patch.Signals) takes its own locks
// and we don't want the registry lock to gate that.
func (a *App) snapshotContexts() []*Ctx {
	a.contextRegistryMu.RLock()
	ctxs := make([]*Ctx, 0, len(a.contextRegistry))
	for _, c := range a.contextRegistry {
		ctxs = append(ctxs, c)
	}
	a.contextRegistryMu.RUnlock()
	return ctxs
}
