package via

// reconnectInit is the client-side reconnect manager injected into every page
// as a Datastar data-init expression (unless WithoutSSEReconnect). It watches
// the global `datastar-fetch` lifecycle events Datastar dispatches for its SSE
// fetch:
//
//   - retrying       → the stream dropped and Datastar is re-attempting; show a
//     "Reconnecting…" banner so the freeze is visible, not silent.
//   - started/finished → a healthy fetch; clear the banner.
//   - retries-failed → Datastar gave up (a graceful-deploy clean close, or a
//     persistent failure / session mismatch that would otherwise leave the tab
//     frozen forever). Reload to re-bootstrap a fresh stream + session, after a
//     jittered delay so a fleet of tabs doesn't stampede the new pod at once.
//
// It also clears the banner on any incoming SSE patch (datastar-patch-elements
// / datastar-patch-signals): a long-lived SSE stream fires 'retrying' on a drop
// but emits NO 'started'/'finished' on a successful resume, so an arrived patch
// (the reconnect re-bootstrap, or via's periodic heartbeat) is the only
// reliable "stream is alive again" signal. Without it the banner stays stuck.
//
// It also publishes connection status as a data-via-connection attribute on the
// <html> element — "online", "connecting", or "offline" — so an app can style
// its OWN connection UI in CSS (e.g. html[data-via-connection="offline"] .banner
// {display:block}) without depending on via's built-in banner. A DOM attribute,
// not a Datastar signal, because Datastar exposes no supported way to merge a
// signal from outside its own fetch lifecycle.
//
// A sessionStorage counter bounds reloads to 3 per failure episode so a server
// that stays down can't pin the tab in a reload loop; a successful load clears
// it after the page has been stable for a few seconds. It is a single IIFE so a
// double injection (e.g. a re-bootstrap) is a no-op via the window guard.
const reconnectInit = `(()=>{if(window.__viaRC)return;window.__viaRC=1;` +
	`var K='__via_rc_reloads',b;` +
	`function conn(s){document.documentElement.setAttribute('data-via-connection',s)}` +
	`conn('online');` +
	`function show(m){if(!b){b=document.createElement('div');b.id='via-reconnect-banner';` +
	`b.setAttribute('role','status');b.setAttribute('aria-live','polite');b.style.cssText='position:fixed;top:0;left:0;right:0;` +
	`z-index:2147483647;padding:.5rem 1rem;text-align:center;font:14px system-ui,sans-serif;` +
	`background:#b45309;color:#fff';(document.body||document.documentElement).appendChild(b)}` +
	`b.textContent=m;b.style.display='block'}` +
	`function hide(){if(b)b.style.display='none'}` +
	`function ok(){conn('online');hide()}` +
	// An incoming SSE patch (the re-bootstrap on reconnect, or via's periodic
	// heartbeat) is the only reliable "stream is alive again" signal: a
	// long-lived SSE @get fires 'retrying' on a drop but NO 'started'/'finished'
	// on a successful resume. The bundled Datastar surfaces incoming patches
	// solely as 'datastar-fetch' events whose detail.type is the patch kind —
	// it never dispatches document-level 'datastar-patch-*' events — so the
	// patch kinds must be matched here or the banner sticks forever and its
	// full-width overlay swallows clicks (proven by
	// TestBrowser_reconnectBannerClearsOnResume).
	`document.addEventListener('datastar-fetch',function(e){var t=e.detail&&e.detail.type;` +
	`if(t==='retrying'){conn('connecting');show('Reconnecting...')}` +
	`else if(t==='started'||t==='finished'||t==='datastar-patch-elements'||t==='datastar-patch-signals'){ok()}` +
	`else if(t==='retries-failed'){conn('offline');var n=0;try{n=+(sessionStorage.getItem(K)||0)}catch(_){}` +
	`if(n>=3){show('Connection lost. Please refresh the page.');return}` +
	`show('Connection lost - reconnecting...');try{sessionStorage.setItem(K,n+1)}catch(_){}` +
	`setTimeout(function(){location.reload()},500+Math.floor(Math.random()*1500))}});` +
	`addEventListener('load',function(){setTimeout(function(){try{sessionStorage.removeItem(K)}catch(_){}},5000)})})()`
