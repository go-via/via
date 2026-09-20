// inspector.js fills every [data-inspector] pane with the signals and the SSE
// frames its own demo produced: it wraps fetch, tees every Datastar response,
// and routes each frame to the pane whose child container it targets.

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
    if (typeof s !== "string") return "";
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

  // One SSE frame per blank line; via splits long fragments over several
  // `data: elements` lines, so those rejoin (stream.go writeElementLines).
  function parseFrame(block, url) {
    var f = { kind: "frame", url: url, event: "", selector: "", mode: "", elements: "", signals: "" };
    var lines = block.split("\n");
    for (var i = 0; i < lines.length; i++) {
      var line = lines[i];
      if (line.indexOf("event:") === 0) {
        f.event = line.slice(6).trim();
      } else if (line.indexOf("data:") === 0) {
        var rest = line.slice(5).replace(/^ /, "");
        var sp = rest.indexOf(" ");
        var key = sp === -1 ? rest : rest.slice(0, sp);
        var val = sp === -1 ? "" : rest.slice(sp + 1);
        if (key === "elements") f.elements += (f.elements ? "\n" : "") + val;
        else if (key === "selector") f.selector = val;
        else if (key === "mode") f.mode = val;
        else if (key === "signals") f.signals = val;
      }
    }
    return f.event ? f : null;
  }

  // Reads a clone of the response, so the long-lived /_via/sse stream is
  // observed frame by frame without Datastar's own reader ever waiting on us.
  function tee(res, url) {
    if (!res.body) return;
    var reader;
    try {
      reader = res.clone().body.getReader();
    } catch (err) {
      return;
    }
    var dec = new TextDecoder();
    var buf = "";
    function step(chunk) {
      if (chunk.done) return;
      buf += dec.decode(chunk.value, { stream: true });
      var parts = buf.split(/\r?\n\r?\n/);
      buf = parts.pop();
      for (var i = 0; i < parts.length; i++) {
        var f = parseFrame(parts[i], url);
        if (f) {
          f.elements = clip(f.elements);
          record(f);
        }
      }
      return reader.read().then(step);
    }
    reader.read().then(step).catch(function () {});
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
        tee(res, url);
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

  // A demo's child container is #via-i{key} (child.go); the first one in the
  // card is the demo's own, any deeper one belongs to a nested child.
  function target(panel) {
    var card = panel.closest(".demo");
    if (!card) return null;
    var box = card.querySelector('[id^="via-i"]');
    if (!box) return null;
    return { id: box.id, key: box.id.slice("via-i".length), box: box };
  }

  function matches(e, t) {
    if (e.kind === "req" || e.kind === "res") return e.url.indexOf("/_via/a/" + t.key + "/") !== -1;
    if (e.selector === "#" + t.id) return true;
    return e.elements.indexOf('id="' + t.id + '"') !== -1;
  }

  function header(e) {
    var time = new Date(e.t).toLocaleTimeString();
    if (e.kind === "req") return time + "  → " + e.method + " " + path(e.url);
    if (e.kind === "res") return time + "  ← " + e.status + " " + path(e.url);
    return time + "  ⇣ " + e.event + (e.mode ? " (" + e.mode + ")" : "");
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

  // The signal names a child declares are all prefixed with its scope
  // (greeting__name, _toggle__open), and the container announces them in its
  // data-signals attribute — so the pane's filter is those prefixes.
  function signalFilter(box) {
    var raw = box.getAttribute("data-signals");
    if (!raw) return "";
    var keys;
    try {
      keys = Object.keys(JSON.parse(raw));
    } catch (err) {
      return "";
    }
    var pre = [];
    for (var i = 0; i < keys.length; i++) {
      var cut = keys[i].indexOf("__");
      var p = (cut === -1 ? keys[i] : keys[i].slice(0, cut + 2)).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      if (pre.indexOf(p) === -1) pre.push(p);
    }
    // JSON, not a JS object literal: Datastar JSON.parses the attribute first
    // and only falls back to the Function constructor, and a string include is
    // turned into a RegExp for it.
    return pre.length ? JSON.stringify({ include: "^(" + pre.join("|") + ")" }) : "";
  }

  function build(panel, t) {
    panel.textContent = "";
    var sig = el("div", "insp-signals");
    sig.appendChild(el("h4", null, "Signals"));
    var filter = signalFilter(t.box);
    if (filter) {
      var pre = el("pre");
      pre.setAttribute("data-json-signals", filter);
      sig.appendChild(pre);
    } else {
      sig.appendChild(el("pre", null, "(this demo declares no client signals)"));
    }
    panel.appendChild(sig);
    var log = el("div", "insp-log");
    panel.appendChild(log);
    return log;
  }

  var panels = [];

  function refresh(p) {
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
      var p = { el: nodes[i], t: t, log: build(nodes[i], t) };
      panels.push(p);
      refresh(p);
    }
  }

  wrapFetch();
  document.addEventListener("DOMContentLoaded", mount);
  document.addEventListener("via:inspect", function () {
    for (var i = 0; i < panels.length; i++) refresh(panels[i]);
  });
})();
