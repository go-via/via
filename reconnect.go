package via

// reconnectInit is the client-side reconnect manager injected into every live
// page as a hash-admitted inline script (see csp.go). It watches Datastar's
// `datastar-fetch` lifecycle events: 'retrying' shows a banner so the freeze is
// visible, 'started'/'finished' clear it, and 'retries-failed' (a
// graceful-deploy clean close, or a persistent failure that would leave the tab
// frozen forever) reloads after a jittered delay so a fleet of tabs doesn't
// stampede the new pod.
//
// It also publishes status as a data-via-connection attribute on <html> —
// "online"/"connecting"/"offline" — so an app can style its OWN connection UI in
// CSS. A DOM attribute, not a signal, because Datastar exposes no supported way
// to merge a signal from outside its own fetch lifecycle.
//
// A sessionStorage counter bounds reloads to 3 per episode so a server that
// stays down can't pin the tab in a reload loop. One IIFE, so a double injection
// is a no-op via the window guard.
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
	// An incoming patch is the only reliable "stream is alive again" signal: a
	// long-lived SSE @post fires 'retrying' on a drop but NO 'started'/'finished'
	// on a successful resume. The bundled Datastar surfaces incoming patches
	// solely as 'datastar-fetch' events whose detail.type is the patch kind — it
	// never dispatches document-level 'datastar-patch-*' events — so those kinds
	// must be matched here or the banner sticks forever and its full-width
	// overlay swallows clicks.
	`document.addEventListener('datastar-fetch',function(e){var t=e.detail&&e.detail.type;` +
	`if(t==='retrying'){conn('connecting');show('Reconnecting...')}` +
	`else if(t==='started'||t==='finished'||t==='datastar-patch-elements'||t==='datastar-patch-signals'){ok()}` +
	`else if(t==='retries-failed'){conn('offline');var n=0;try{n=+(sessionStorage.getItem(K)||0)}catch(_){}` +
	`if(n>=2){show('Connection lost. Please refresh the page.');return}` +
	`show('Connection lost - reconnecting...');try{sessionStorage.setItem(K,n+1)}catch(_){}` +
	`setTimeout(function(){location.reload()},500+Math.floor(Math.random()*1500))}});` +
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
