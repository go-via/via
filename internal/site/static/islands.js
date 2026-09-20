// The islands page's JavaScript: two functions Go calls through data-effect,
// and the teardown every island needs. via never generates any of this.
//
// Both entry points must survive being called again with no argument change:
// a data-effect re-runs whenever any signal it reads changes.

maplibregl.setWorkerUrl("/static/vendor/maplibre-gl-csp-worker.js");

// One same-origin GeoJSON file, no tiles, glyphs or sprite, so the page's
// default-src 'self' needs no widening for the map to draw.
const STYLE = {
  version: 8,
  sources: {
    world: {type: "geojson", data: "/static/data/world-lowres.geojson"},
  },
  layers: [
    {id: "sea", type: "background", paint: {"background-color": "#16181d"}},
    {id: "land", type: "fill", source: "world", paint: {"fill-color": "#2a2e37"}},
    {id: "coast", type: "line", source: "world", paint: {"line-color": "#ffbf00", "line-width": 0.6}},
  ],
};

// ??= is what makes the effect idempotent: the first run builds the map, every
// run after it is a jumpTo.
window.viaMap = (el, view) => {
  el._map ??= new maplibregl.Map({
    container: el,
    style: STYLE,
    interactive: true,
    attributionControl: false,
  });
  el._map.jumpTo({center: view.center, zoom: view.zoom});
};

window.viaChart = (el, series) => {
  const c = el.getContext("2d");
  const w = el.width, h = el.height;
  c.clearRect(0, 0, w, h);
  if (!Array.isArray(series) || series.length === 0) return;
  const max = Math.max(...series, 1);
  const step = w / series.length;
  c.fillStyle = "#ffbf00";
  series.forEach((v, i) => {
    const bar = (v / max) * (h - 2);
    c.fillRect(i * step + 1, h - bar, Math.max(step - 2, 1), bar);
  });
  c.strokeStyle = "#2a2e37";
  c.beginPath();
  c.moveTo(0, h - 0.5);
  c.lineTo(w, h - 0.5);
  c.stroke();
};

// Teardown is userland: via has no unmount hook, so the page watches the DOM
// for the nodes a patch removed and frees the WebGL context and worker each
// map holds. One observer for the document, not one per island.
new MutationObserver((records) => {
  for (const r of records) {
    for (const node of r.removedNodes) {
      if (node.nodeType !== Node.ELEMENT_NODE) continue;
      const gone = node.matches("[data-island-map]") ? [node] : node.querySelectorAll("[data-island-map]");
      for (const el of gone) {
        if (!el._map) continue;
        el._map.remove();
        el._map = null;
      }
    }
  }
}).observe(document, {childList: true, subtree: true});
