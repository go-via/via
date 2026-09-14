package via

// reconnectInit is the client-side reconnect manager injected into every live
// page as a hash-admitted inline script (see csp.go). It watches Datastar's
// `datastar-fetch` lifecycle events and turns every way a stream can die into
// something the user can see.
//
// The load-bearing fact, read off the bundled datastar.js: the fetch driver
// defaults to retry:"auto", and under "auto" the ONLY path that reaches its
// retry helper is the network-error catch. A response that ends — a clean
// close on a graceful deploy, or Router.Close returning from the stream — takes
// the `u?.(),h==="always"&&!mt` test, fails it, and falls through to `q(),n()`:
// it fires `finished` and never `retrying`/`retries-failed`. A non-200 status
// likewise falls straight to `q(),n()` under "auto". So `retries-failed` covers
// network drops only, and a clean close would otherwise leave a page that looks
// alive while every click 410s.
//
// Hence: `finished` whose detail.el is <body> IS the stream ending — the SSE
// @post is the only fetch mounted on <body> (router.go's data-init) — and it is
// treated as a drop. Emitting retry:"always" on that @post was the alternative
// and is worse: it would also retry the deliberate 404 "no stream for this tab"
// ten times with backoff before giving up, and it still reports the give-up
// through the same handler, so it buys nothing this does not already do.
//
// `error` is dispatched from the driver's onopen for any status >= 400, with
// the code as a STRING in detail.argsRaw.status. A 410 means the tab is stale
// (its stream is gone, or the render no longer binds the action) — reload. A
// 403/5xx is a server-side condition a reload will not fix — banner only.
//
// It also publishes status as a data-via-connection attribute on <html> —
// "online"/"connecting"/"offline" — so an app can style its OWN connection UI in
// CSS. A DOM attribute, not a signal, because Datastar exposes no supported way
// to merge a signal from outside its own fetch lifecycle.
//
// A sessionStorage counter bounds reloads to 3 per episode so a server that
// stays down can't pin the tab in a reload loop. One IIFE, so a double injection
// is a no-op via the window guard.
//
// The give-up used to be a single blind location.reload() on a 500-2000ms
// timer. A deploy's gap is seconds, not milliseconds, so that reload landed on
// chrome-error://chromewebdata with no script left to retry — a permanently
// dead tab on every rolling restart. It now probes the page URL with capped
// exponential backoff and reloads only once the server answers.
const reconnectInit = `(()=>{if(window.__viaRC)return;window.__viaRC=1;` +
	`var K='__via_rc_reloads',b,gen=0,fails=0;` +
	`function conn(s){document.documentElement.setAttribute('data-via-connection',s)}` +
	`conn('online');` +
	`function show(m){if(!b){b=document.createElement('div');b.id='via-reconnect-banner';` +
	`b.setAttribute('role','status');b.setAttribute('aria-live','polite');b.style.cssText='position:fixed;top:0;left:0;right:0;` +
	`z-index:2147483647;padding:.5rem 1rem;text-align:center;font:14px system-ui,sans-serif;` +
	`background:#b45309;color:#fff';(document.body||document.documentElement).appendChild(b)}` +
	`b.textContent=m;b.style.display='block'}` +
	`function hide(){if(b)b.style.display='none'}` +
	`function ok(){gen++;fails=0;conn('online');hide()}` +
	`function stop(m){gen++;conn('offline');show(m)}` +
	// The re-bootstrap PROBES before it reloads. A reload fired blind lands on
	// chrome-error://chromewebdata the moment the server is still down — which
	// every real deploy is, for seconds — and the browser gives up there with
	// no script left alive to try again. So: ask for this page until it answers,
	// backing off 500ms → 8s, and only then reload. gen cancels an in-flight
	// probe the instant a real patch proves the stream came back on its own.
	`function probe(d,g){setTimeout(function(){if(g!==gen)return;` +
	`fetch(location.href,{cache:'no-store',credentials:'same-origin'}).then(function(r){` +
	`if(g!==gen)return;if(!r.ok)throw 0;` +
	`try{sessionStorage.setItem(K,+(sessionStorage.getItem(K)||0)+1)}catch(_){}` +
	`location.reload()}).catch(function(){if(g!==gen)return;` +
	`if(++fails>20){stop('Connection lost. Please refresh the page.');return}` +
	`probe(Math.min(d*2,8000),g)})},d+Math.floor(Math.random()*250))}` +
	`function lost(m){conn('offline');var n=0;try{n=+(sessionStorage.getItem(K)||0)}catch(_){}` +
	`if(n>=2){show('Connection lost. Please refresh the page.');return}` +
	`show(m);fails=0;probe(500,++gen)}` +
	// An incoming patch is the only reliable "stream is alive again" signal: a
	// long-lived SSE @post fires 'retrying' on a drop but NO 'started'/'finished'
	// on a successful resume. The bundled Datastar surfaces incoming patches
	// solely as 'datastar-fetch' events whose detail.type is the patch kind — it
	// never dispatches document-level 'datastar-patch-*' events — so those kinds
	// must be matched here or the banner sticks forever and its full-width
	// overlay swallows clicks.
	`document.addEventListener('datastar-fetch',function(e){var d=e.detail||{},t=d.type;` +
	`if(t==='retrying'){conn('connecting');show('Reconnecting...')}` +
	`else if(t==='error'){var s=+((d.argsRaw||{}).status||0);` +
	`if(s===410){lost('Page is out of date - reloading...')}` +
	`else if(s===403||s>=500){stop('Connection lost. Please refresh the page.')}}` +
	// detail.el is <body> only for the SSE @post in data-init; an action POST
	// finishing on some button must still clear the banner.
	`else if(t==='finished'){if(d.el===document.body){lost('Connection lost - reconnecting...')}else{ok()}}` +
	`else if(t==='started'||t==='datastar-patch-elements'||t==='datastar-patch-signals'){ok()}` +
	`else if(t==='retries-failed'){lost('Connection lost - reconnecting...')}});` +
	`addEventListener('load',function(){setTimeout(function(){try{sessionStorage.removeItem(K)}catch(_){}},5000)})})()`

// reconnectScript renders the reconnect manager as an inline <script>, or ""
// when off. The CSP admits it by SHA-256 of reconnectInit, so an edit re-derives
// the hash — but the emitted text must stay BYTE-IDENTICAL to reconnectInit. Add
// so much as a newline around it and the browser silently drops the script, and
// the tab freezes on a drop exactly when this manager was meant to recover it.
func reconnectScript(on bool) string {
	if !on {
		return ""
	}
	return `<script>` + reconnectInit + `</script>`
}
