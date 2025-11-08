package main

import (
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

func LiveReloadPlugin(v *via.V) {
	v.HandleFunc("GET /dev/reload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		<-r.Context().Done()
	})
}

func liveReloadScript() h.H {
	return h.Script(h.Raw(`
if (window.location.hostname === 'localhost' || window.location.hostname === '127.0.0.1') {
  const evtSource = new EventSource('/dev/reload');
  let overlay = null;
  let showTimer = null;
  
  evtSource.onerror = () => {
    evtSource.close();
    
    showTimer = setTimeout(() => {
      if (!overlay) {
        overlay = document.createElement('div');
        overlay.style.cssText = 'position: fixed; top: 50%; left: 50%; transform: translate(-50%, -50%); background: rgba(200, 200, 200, 0.95); padding: 20px 40px; border-radius: 8px; color: #333; font-size: 24px; z-index: 999999; font-family: -apple-system, sans-serif;';
        overlay.textContent = '🔌 Reconnecting...';
        document.body.appendChild(overlay);
      }
    }, 1000);
    
    (async function poll() {
      for (let i = 0; i < 100; i++) {
        try {
          const res = await fetch('/', { method: 'HEAD', signal: AbortSignal.timeout(1000) });
          if (res.ok) {
            clearTimeout(showTimer);
            if (overlay) overlay.remove();
            location.reload();
            return;
          }
        } catch (e) {}
        await new Promise(r => setTimeout(r, 50));
      }
    })();
  };
}
`))
}
