// Called through data-effect, which re-runs on any signal change: both must be idempotent.

(function () {
  const css = getComputedStyle(document.documentElement);
  const color = (name, fallback) => css.getPropertyValue(name).trim() || fallback;
  const BG = color("--bg", "#16181d");
  const BORDER = color("--border", "#2a2e37");
  const AMBER = color("--amber", "#ffbf00");

  window.viaChart = (el, series) => {
    const c = el.getContext("2d");
    if (!c) return;
    const w = el.width, h = el.height;
    c.clearRect(0, 0, w, h);
    if (!Array.isArray(series) || series.length === 0) return;
    const max = Math.max(...series, 1);
    const step = w / series.length;
    c.fillStyle = AMBER;
    series.forEach((v, i) => {
      const bar = (v / max) * (h - 2);
      c.fillRect(i * step + 1, h - bar, Math.max(step - 2, 1), bar);
    });
    c.strokeStyle = BORDER;
    c.beginPath();
    c.moveTo(0, h - 0.5);
    c.lineTo(w, h - 0.5);
    c.stroke();
  };

  // Same-origin GeoJSON only, so default-src 'self' needs no widening.
  const STYLE = {
    version: 8,
    sources: {
      world: {type: "geojson", data: "/static/data/world-lowres.geojson"},
    },
    layers: [
      {id: "sea", type: "background", paint: {"background-color": BG}},
      {id: "land", type: "fill", source: "world", paint: {"fill-color": BORDER}},
      {id: "coast", type: "line", source: "world", paint: {"line-color": AMBER, "line-width": 0.6}},
    ],
  };

  if (window.maplibregl) {
    maplibregl.setWorkerUrl("/static/vendor/maplibre-gl-csp-worker.js");
  }

  // First run builds and starts watching the container; later runs jumpTo.
  window.viaMap = (el, view) => {
    if (!window.maplibregl) {
      el.textContent = "map library failed to load";
      return;
    }
    if (!el._map) {
      el._map = new maplibregl.Map({
        container: el,
        style: STYLE,
        interactive: true,
        attributionControl: false,
      });
      // Adding or removing a map reflows the grid: MapLibre only reads the
      // container size on its own resize event, which a reflow does not fire.
      el._ro = new ResizeObserver(() => el._map && el._map.resize());
      el._ro.observe(el);
      // The container is the map's own element, so the message cannot be
      // written into it; islands.css renders the attribute.
      el._map.on("error", (ev) => {
        el.setAttribute("data-error", "map failed to load: " + ((ev && ev.error && ev.error.message) || "unknown error"));
      });
    }
    el._map.jumpTo({center: view.center, zoom: view.zoom});
  };

  // via reports removals on via:remove; freeing the WebGL context and worker of
  // a removed map is still ours to do.
  document.addEventListener("via:remove", (e) => {
    const root = e.detail.el;
    const gone = root.matches("[data-island-map]") ? [root] : root.querySelectorAll("[data-island-map]");
    for (const el of gone) {
      if (!el._map) continue;
      if (el._ro) {
        el._ro.disconnect();
        el._ro = null;
      }
      el._map.remove();
      el._map = null;
    }
  });
})();
