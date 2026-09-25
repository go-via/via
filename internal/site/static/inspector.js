// Fills each [data-inspector] pane (a demo card's Wire view) with its own
// demo's signals, action POSTs and applied patches, newest first.

(function () {
  "use strict";

  var MAX_ENTRIES = 200;
  var MAX_BODY = 2048;
  var EMPTY = "Use the demo to see its requests and patches here.";

  // What each status via answers an action or stream POST with means, from
  // dispatch.go and connect.go. 200 and 204 are success and get no note.
  var STATUS = {
    400: "bad request: an argument or body this page never rendered",
    403: "forbidden: the Origin is not trusted, or the session changed under the tab",
    404: "not found: no such route, or no stream for this tab",
    410: "gone: the server no longer holds what this request names; reload",
    413: "request body over the size cap",
    500: "the handler panicked; the server logged it",
    503: "unavailable: the tab's goroutine is busy, the stream cap is reached or the session store did not answer; retry",
  };

  // via answers 410 for several stale addresses; its body says which one
  // (dispatch.go: "no such child", unknownAction, unrenderedArg.body, noStream).
  var GONE = [
    ["no such child", "gone: no child at this key now; a sibling before it came or went, so this URL is stale; reload"],
    ["no such action", "gone: the server's render no longer binds this action; this page's markup is older; reload"],
    ["this render does not bind that action for that argument", "gone: the action is bound, but not for this argument; the row changed since this page rendered; reload"],
    ["stream closed", "gone: this tab's stream closed before the action ran; reload"],
    ["request abandoned", "gone: the stream shut down with this action still queued; retry"],
    ["this tab id has no open stream", "gone: no open stream for this tab id (the connection closed or the server restarted); reload"],
    ["this action needs the page's tab id", "gone: the request carried no tab id; the page never opened its stream"],
  ];

  function goneWhy(body) {
    for (var i = 0; i < GONE.length; i++) if (body.indexOf(GONE[i][0]) === 0) return GONE[i][1];
    return "";
  }

  var seq = 0;

  var entries = [];

  function record(e) {
    e.t = Date.now();
    e.id = ++seq;
    entries.push(e);
    if (entries.length > MAX_ENTRIES) entries.shift();
    document.dispatchEvent(new CustomEvent("via:inspect", { detail: e }));
  }

  // A request is one row: the response fills in its status and time.
  function settle(e, status, answer) {
    e.status = status;
    if (answer) e.answer = clip(answer.trim());
    e.ms = Math.round(performance.now() - e.t0);
    document.dispatchEvent(new CustomEvent("via:inspect-settle", { detail: e }));
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
      var e = { kind: "req", url: url, method: method, body: bodyOf(init), t0: performance.now() };
      record(e);
      return orig(input, init).then(
        function (res) {
          if (res.status !== 410) {
            settle(e, res.status);
            return res;
          }
          return res
            .clone()
            .text()
            .then(
              function (body) {
                settle(e, res.status, body);
              },
              function () {
                settle(e, res.status);
              },
            )
            .then(function () {
              return res;
            });
        },
        function (err) {
          settle(e, "failed");
          throw err;
        },
      );
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

  // #via-i{key} (child.go): every outermost one in the card, since a card may
  // render several children side by side. Only id and key are kept: a patch
  // can replace the node.
  function targets(panel) {
    var card = panel.closest(".demo");
    if (!card) return [];
    var all = card.querySelectorAll('[id^="via-i"]');
    var out = [];
    var kept = [];
    for (var i = 0; i < all.length; i++) {
      var inside = false;
      for (var j = 0; j < kept.length; j++) if (kept[j].contains(all[i])) inside = true;
      if (inside) continue;
      kept.push(all[i]);
      out.push({ id: all[i].id, key: all[i].id.slice("via-i".length) });
    }
    return out;
  }

  function isStream(url) {
    return url.indexOf("/_via/sse") !== -1;
  }

  // A nested child's key and id extend its parent's with "-n", so a demo's
  // pane owns its children's traffic too.
  function owns(parent, key) {
    return key === parent || key.indexOf(parent + "-") === 0;
  }

  function matches(e, p) {
    // The stream is per page, not per demo, so every pane shows it.
    if (e.kind === "req" && isStream(e.url)) return true;
    for (var i = 0; i < p.boxes.length; i++) if (matchesBox(e, p.boxes[i])) return true;
    if (e.kind === "req" || !p.signals) return false;
    for (var j = 0; j < e.names.length; j++) if (p.signals.test(e.names[j])) return true;
    return false;
  }

  function matchesBox(e, t) {
    if (e.kind === "req") {
      var m = /\/_via\/a\/([^/]+)\//.exec(e.url);
      return !!m && owns(t.key, m[1]);
    }
    if (e.selector === "#" + t.id || e.selector.indexOf("#" + t.id + "-") === 0) return true;
    return e.elements.indexOf('id="' + t.id + '"') !== -1 || e.elements.indexOf('id="' + t.id + '-') !== -1;
  }

  function span(parent, cls, text) {
    var n = el("span", cls, text);
    parent.appendChild(n);
    return n;
  }

  function statusClass(st) {
    if (typeof st !== "number") return "w-bad";
    return st < 300 ? "w-ok" : st < 500 ? "w-warn" : "w-bad";
  }

  // The row a reader scans: when, what, where, and the outcome. A request's
  // outcome arrives later, so it has its own cell that settle refills.
  function summary(e) {
    var s = el("summary");
    span(s, "w-time", new Date(e.t).toLocaleTimeString([], { hour12: false }));
    if (e.kind === "frame") {
      span(s, "w-kind", e.event);
      span(s, "w-target", e.target || "(no target)");
      span(s, "w-meta", bytes(e.bytes) + " · " + e.via);
      return s;
    }
    span(s, "w-kind", e.method);
    span(s, "w-target", path(e.url) + (isStream(e.url) ? " (stream)" : ""));
    outcome(span(s, "w-meta"), e);
    return s;
  }

  function outcome(cell, e) {
    cell.textContent = "";
    if (e.status == null) {
      cell.textContent = "pending";
      return;
    }
    span(cell, "w-status " + statusClass(e.status), String(e.status));
    cell.appendChild(document.createTextNode(" · " + e.ms + " ms"));
    var why = (e.status === 410 && e.answer && goneWhy(e.answer)) || STATUS[e.status] || (e.status === "failed" ? "the request never got an answer: offline or refused" : "");
    var row = cell.parentNode;
    if (why && !row.querySelector(".w-note")) span(row, "w-note", e.status + " " + why);
  }

  function bytes(n) {
    return n < 1024 ? n + " B" : (n / 1024).toFixed(1) + " KB";
  }

  function path(u) {
    try {
      return new URL(u, location.href).pathname;
    } catch (err) {
      return u;
    }
  }

  function payload(e) {
    if (e.kind === "req") return e.url + "\n" + (e.body || "(no body)") + (e.answer ? "\nanswer: " + e.answer : "");
    var out = [];
    if (e.selector) out.push("selector " + e.selector);
    if (e.mode) out.push("mode " + e.mode);
    if (e.signals) out.push(e.signals);
    if (e.elements) out.push(e.elements);
    return out.join("\n");
  }

  function entryNode(e) {
    var wrap = el("details", "insp-e insp-" + e.kind);
    wrap.setAttribute("data-e", e.id);
    wrap.appendChild(summary(e));
    wrap.appendChild(el("pre", null, payload(e)));
    return wrap;
  }

  // A child's signals carry its scope prefix, announced in data-signals. The
  // pattern both filters the live signal view and claims patch-signals frames.
  function signalPattern(boxes) {
    var keys = [];
    for (var b = 0; b < boxes.length; b++) {
      var box = document.getElementById(boxes[b].id);
      var raw = box && box.getAttribute("data-signals");
      if (!raw) continue;
      try {
        keys = keys.concat(Object.keys(JSON.parse(raw)));
      } catch (err) {}
    }
    if (!keys.length) return "";
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
    return alts.join("|");
  }

  function build(panel, pattern) {
    panel.textContent = "";
    var sig = el("div", "insp-signals");
    sig.appendChild(el("h4", null, "Signals"));
    if (pattern) {
      var pre = el("pre");
      // JSON, not a JS object literal: Datastar JSON.parses the attribute
      // first and only falls back to the Function constructor, and a string
      // include is turned into a RegExp for it.
      pre.setAttribute("data-json-signals", JSON.stringify({ include: pattern }));
      sig.appendChild(pre);
    } else {
      sig.appendChild(el("pre", null, "(this demo declares no client signals)"));
    }
    panel.appendChild(sig);
    var traffic = el("div", "insp-traffic");
    traffic.appendChild(el("h4", null, "Requests and patches, newest first"));
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
    if (!matches(e, p)) return;
    var empty = p.log.querySelector(".insp-empty");
    if (empty) empty.remove();
    var node = entryNode(e);
    p.log.insertBefore(node, p.log.firstChild);
    // offsetHeight is 0 while the pane is hidden, which would scroll
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
      if (matches(entries[i], p)) {
        p.log.appendChild(entryNode(entries[i]));
        n++;
      }
    }
    if (!n) p.log.appendChild(el("p", "insp-empty", EMPTY));
  }

  function mount() {
    var nodes = document.querySelectorAll("[data-inspector]");
    for (var i = 0; i < nodes.length; i++) {
      var boxes = targets(nodes[i]);
      if (!boxes.length) continue;
      var pattern = signalPattern(boxes);
      nodes[i].tabIndex = 0; // a scroll container is unreachable by keyboard otherwise
      var p = { el: nodes[i], boxes: boxes, signals: pattern ? new RegExp(pattern) : null, log: build(nodes[i], pattern) };
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
    var names = [];
    if (d.signals) {
      try {
        names = Object.keys(JSON.parse(d.signals));
      } catch (err) {}
    }
    var id = /\bid="([^"]+)"/.exec(d.elements);
    record({
      kind: "frame",
      event: "patch-" + d.kind,
      selector: d.selector,
      mode: d.mode,
      names: names,
      target: d.kind === "signals" ? names.join(", ") : d.selector || (id ? "#" + id[1] : ""),
      bytes: new TextEncoder().encode(d.elements || d.signals).length,
      // via's data-init puts the stream's fetch on <body>; any other element
      // is the control whose action answered with this patch.
      via: d.el === document.body ? "stream" : "response",
      elements: clip(d.elements),
      signals: clip(d.signals),
    });
  });

  document.addEventListener("via:inspect", function (ev) {
    for (var i = 0; i < panels.length; i++) prepend(panels[i], ev.detail);
  });

  document.addEventListener("via:inspect-settle", function (ev) {
    var e = ev.detail;
    for (var i = 0; i < panels.length; i++) {
      var row = panels[i].log.querySelector('[data-e="' + e.id + '"]');
      if (!row) continue;
      outcome(row.querySelector("summary .w-meta"), e);
      row.querySelector("pre").textContent = payload(e);
    }
  });
})();
