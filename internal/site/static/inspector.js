// Fills each [data-inspector] pane with the frames and signals of its own demo.

(function () {
  "use strict";

  var MAX_ENTRIES = 200;
  var MAX_BODY = 2048;
  var EMPTY = "Interact with the demo to see requests, frames and signals here.";

  var entries = [];

  function record(e) {
    e.t = Date.now();
    entries.push(e);
    if (entries.length > MAX_ENTRIES) entries.shift();
    document.dispatchEvent(new CustomEvent("via:inspect", { detail: e }));
  }

  function clip(s) {
    return s.length > MAX_BODY ? s.slice(0, MAX_BODY) + "\n… truncated" : s;
  }

  // Datastar sends its headers as a plain object ({"Datastar-Request": true});
  // a Request input carries a Headers instead.
  function isDatastar(input, init) {
    var h = (init && init.headers) || (input && input.headers);
    if (!h) return false;
    if (typeof h.get === "function") return h.get("Datastar-Request") != null;
    for (var k in h) if (k.toLowerCase() === "datastar-request") return true;
    return false;
  }

  function urlOf(input) {
    if (typeof input === "string") return input;
    if (input && typeof input.url === "string") return input.url;
    return String(input);
  }

  function bodyOf(init) {
    var b = init && init.body;
    if (!b) return "";
    if (typeof b === "string") return clip(b);
    if (b instanceof FormData) {
      var names = [];
      b.forEach(function (_, k) {
        names.push(k);
      });
      return "multipart fields: " + names.join(", ");
    }
    return clip(String(b));
  }

  function wrapFetch() {
    if (window.fetch.viaInspect) return;
    var orig = window.fetch;
    var wrapped = function (input, init) {
      if (!isDatastar(input, init)) return orig(input, init);
      var url = urlOf(input);
      var method = (init && init.method) || (input && input.method) || "GET";
      record({ kind: "req", url: url, method: method, body: bodyOf(init) });
      return orig(input, init).then(function (res) {
        record({ kind: "res", url: url, method: method, status: res.status });
        return res;
      });
    };
    wrapped.viaInspect = true;
    window.fetch = wrapped;
  }

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }

  // #via-i{key} (child.go). Only id and key are kept: a patch can replace the node.
  function target(panel) {
    var card = panel.closest(".demo");
    if (!card) return null;
    var box = card.querySelector('[id^="via-i"]');
    if (!box) return null;
    return { id: box.id, key: box.id.slice("via-i".length) };
  }

  function isStream(url) {
    return url.indexOf("/_via/sse") !== -1;
  }

  // A nested child's key and id extend its parent's with "-n", so a demo's
  // pane owns its children's traffic too.
  function owns(parent, key) {
    return key === parent || key.indexOf(parent + "-") === 0;
  }

  function matches(e, t) {
    if (e.kind === "req" || e.kind === "res") {
      // The stream is per page, not per demo, so every pane shows it.
      if (isStream(e.url)) return true;
      var m = /\/_via\/a\/([^/]+)\//.exec(e.url);
      return !!m && owns(t.key, m[1]);
    }
    if (e.selector === "#" + t.id || e.selector.indexOf("#" + t.id + "-") === 0) return true;
    return e.elements.indexOf('id="' + t.id + '"') !== -1 || e.elements.indexOf('id="' + t.id + '-') !== -1;
  }

  function header(e) {
    var time = new Date(e.t).toLocaleTimeString();
    // A frame arrives on via:patch, which names no URL.
    if (e.kind === "frame") return time + "  ⇣ " + e.event + (e.mode ? " (" + e.mode + ")" : "");
    var tag = isStream(e.url) ? " (stream)" : "";
    if (e.kind === "req") return time + "  → " + e.method + " " + path(e.url) + tag;
    return time + "  ← " + e.status + " " + path(e.url) + tag;
  }

  function path(u) {
    try {
      return new URL(u, location.href).pathname;
    } catch (err) {
      return u;
    }
  }

  function payload(e) {
    if (e.kind === "req") return e.body || "(no body)";
    if (e.kind === "res") return "status " + e.status + "\n" + e.url;
    var out = [];
    if (e.selector) out.push("selector " + e.selector);
    if (e.mode) out.push("mode " + e.mode);
    if (e.signals) out.push(e.signals);
    if (e.elements) out.push(e.elements);
    return out.join("\n");
  }

  function entryNode(e) {
    var wrap = el("details", "insp-e insp-" + e.kind);
    wrap.appendChild(el("summary", null, header(e)));
    wrap.appendChild(el("pre", null, payload(e)));
    return wrap;
  }

  // A child's signals carry its scope prefix, announced in data-signals.
  function signalFilter(t) {
    var box = document.getElementById(t.id);
    var raw = box && box.getAttribute("data-signals");
    if (!raw) return "";
    var keys;
    try {
      keys = Object.keys(JSON.parse(raw));
    } catch (err) {
      return "";
    }
    var pre = [];
    var exact = [];
    for (var i = 0; i < keys.length; i++) {
      var cut = keys[i].indexOf("__");
      var p = (cut === -1 ? keys[i] : keys[i].slice(0, cut + 2)).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      var into = cut === -1 ? exact : pre;
      if (into.indexOf(p) === -1) into.push(p);
    }
    var alts = [];
    // A prefix owns everything under it; an unprefixed key owns only itself,
    // so it is anchored at both ends or a sibling signal leaks into the pane.
    if (pre.length) alts.push("^(?:" + pre.join("|") + ")");
    if (exact.length) alts.push("^(?:" + exact.join("|") + ")$");
    // JSON, not a JS object literal: Datastar JSON.parses the attribute first
    // and only falls back to the Function constructor, and a string include is
    // turned into a RegExp for it.
    return alts.length ? JSON.stringify({ include: alts.join("|") }) : "";
  }

  function build(panel, t) {
    panel.textContent = "";
    var sig = el("div", "insp-signals");
    sig.appendChild(el("h4", null, "Signals"));
    var filter = signalFilter(t);
    if (filter) {
      var pre = el("pre");
      pre.setAttribute("data-json-signals", filter);
      sig.appendChild(pre);
    } else {
      sig.appendChild(el("pre", null, "(this demo declares no client signals)"));
    }
    panel.appendChild(sig);
    var traffic = el("div", "insp-traffic");
    traffic.appendChild(el("h4", null, "Requests, responses and frames"));
    var log = el("div", "insp-log");
    log.setAttribute("role", "log");
    traffic.appendChild(log);
    panel.appendChild(traffic);
    return log;
  }

  var panels = [];

  // Prepending rather than rebuilding: a rebuild would close open <details>
  // and jump the scroll.
  function prepend(p, e) {
    if (!matches(e, p.t)) return;
    var empty = p.log.querySelector(".insp-empty");
    if (empty) empty.remove();
    var node = entryNode(e);
    p.log.insertBefore(node, p.log.firstChild);
    // offsetHeight is 0 while the pane's tab is unchecked, which would scroll
    // the pane to the wrong place on the next visible prepend.
    if (p.el.offsetParent !== null && p.el.scrollTop > 0) p.el.scrollTop += node.offsetHeight;
    // The global ring drops old entries but never the nodes already rendered
    // from them, so each pane trims itself.
    while (p.log.childElementCount > MAX_ENTRIES) p.log.lastElementChild.remove();
  }

  function fill(p) {
    p.log.textContent = "";
    var n = 0;
    for (var i = entries.length - 1; i >= 0; i--) {
      if (matches(entries[i], p.t)) {
        p.log.appendChild(entryNode(entries[i]));
        n++;
      }
    }
    if (!n) p.log.appendChild(el("p", "insp-empty", EMPTY));
  }

  function mount() {
    var nodes = document.querySelectorAll("[data-inspector]");
    for (var i = 0; i < nodes.length; i++) {
      var t = target(nodes[i]);
      if (!t) continue;
      nodes[i].tabIndex = 0; // a scroll container is unreachable by keyboard otherwise
      var p = { el: nodes[i], t: t, log: build(nodes[i], t) };
      panels.push(p);
      fill(p);
    }
    if (panels.length) wrapFetch();
  }

  // At eval, not DOMContentLoaded: via's data-init POSTs /_via/sse before that event.
  mount();

  // via dispatches via:patch for every applied patch, on plain and live pages
  // alike, so the pane needs no reader of its own on the SSE stream.
  document.addEventListener("via:patch", function (ev) {
    if (!panels.length) return;
    var d = ev.detail;
    record({
      kind: "frame",
      event: "datastar-patch-" + d.kind,
      selector: d.selector,
      mode: d.mode,
      elements: clip(d.elements),
      signals: clip(d.signals),
    });
  });

  document.addEventListener("via:inspect", function (ev) {
    for (var i = 0; i < panels.length; i++) prepend(panels[i], ev.detail);
  });
})();
