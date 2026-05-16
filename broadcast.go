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
