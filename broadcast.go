package via

// Broadcast queues a JavaScript snippet on every currently-live tab's
// patch queue. The next SSE drain on each tab pushes it to the browser.
// Useful for "page will reload in 30 seconds" maintenance notices,
// site-wide flash messages, or coordinated state invalidation.
//
//	app.Broadcast(`alert("Maintenance in 30 seconds.")`)
//	time.Sleep(30 * time.Second)
//	app.Shutdown(ctx)
//
// Returns the number of tabs the script was queued on. Empty script is
// a no-op.
func (a *App) Broadcast(script string) int {
	if script == "" {
		return 0
	}
	ctxs := a.snapshotContexts()
	for _, c := range ctxs {
		enqueueScript(c, script)
	}
	return len(ctxs)
}

// BroadcastSignals pushes a signal patch to every currently-live tab.
// Useful for site-wide announcements that drive a banner via a
// client-only signal (e.g. "$_systemNotice = 'planned maintenance'")
// without rendering each composition. Returns the tab count.
func (a *App) BroadcastSignals(values map[string]any) int {
	if len(values) == 0 {
		return 0
	}
	ctxs := a.snapshotContexts()
	for _, c := range ctxs {
		c.PatchSignals(values)
	}
	return len(ctxs)
}

// broadcastRender forces a view re-render on every live *Ctx whose
// most recent render read key, except the writer (skipping it avoids
// re-entering its action mutex). When sess is non-nil only ctxs on
// that session are included — the scope for session-scoped writes
// that must not wake unrelated sessions. The writer's own re-render
// happens through the action's autoflush.
func (a *App) broadcastRender(skip *Ctx, sess *session, key string) {
	for _, c := range a.snapshotContexts() {
		if c == skip {
			continue
		}
		if sess != nil && c.session != sess {
			continue
		}
		if !c.subscribed(key) {
			continue
		}
		go c.Sync()
	}
}

// snapshotContexts copies every live *Ctx into a slice under the
// registry RLock, so callers can iterate without holding the lock —
// the per-Ctx work (enqueueScript, PatchSignals) takes its own locks
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
