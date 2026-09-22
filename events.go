package via

// eventsInit is the inline script every via document ships, hash-admitted by
// csp.go. It publishes two CustomEvents on `document`, so apps never read
// Datastar's own events:
//
//   - `via:patch` after each applied patch, detail {kind, el, selector, mode,
//     elements, signals}; el is the element whose fetch carried the patch (the
//     clicked control, or the data-init element for the stream), not the
//     patched node. Datastar surfaces a frame only as a `datastar-fetch` event
//     (detail.type = patch kind, detail.argsRaw = the data lines as strings)
//     and applies it in its own listener. datastar.js is a deferred module, so
//     this listener registers first; dispatching from a microtask puts it
//     after Datastar's synchronous apply.
//   - `via:remove` for each element that leaves the document, detail.el the
//     removed root. isConnected is re-checked at delivery: a morph moves a
//     node by removing and re-inserting it in one batch. Mutations directly
//     under <html> are skipped: Datastar parks a hidden scratch div there for
//     every patch and files removed id-bearing nodes into it, so each patch
//     would otherwise report that div and everything it has collected.
//
// One IIFE; the window guard makes a double injection a no-op.
const eventsInit = `(()=>{if(window.__viaEV)return;window.__viaEV=1;` +
	`function fire(n,d){document.dispatchEvent(new CustomEvent(n,{detail:d}))}` +
	`document.addEventListener('datastar-fetch',function(e){var d=e.detail||{},t=d.type;` +
	`if(t!=='datastar-patch-elements'&&t!=='datastar-patch-signals')return;` +
	`var a=d.argsRaw||{},k=t==='datastar-patch-elements'?'elements':'signals';` +
	`queueMicrotask(function(){fire('via:patch',{kind:k,el:d.el,selector:a.selector||'',` +
	`mode:a.mode||'outer',elements:a.elements||'',signals:a.signals||''})})});` +
	`new MutationObserver(function(ms){for(var i=0;i<ms.length;i++){if(ms[i].target===document.documentElement)continue;` +
	`var r=ms[i].removedNodes;` +
	`for(var j=0;j<r.length;j++){var n=r[j];if(n.nodeType===1&&!n.isConnected)fire('via:remove',{el:n})}}})` +
	`.observe(document,{childList:true,subtree:true})})()`

// eventsScript renders the event bridge as an inline <script>. Like the
// reconnect manager, the CSP admits it by SHA-256 of eventsInit, so the emitted
// text must stay BYTE-IDENTICAL to the const.
func eventsScript() string { return `<script>` + eventsInit + `</script>` }
