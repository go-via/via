// Site chrome: the version picker's dismissal and the contents rail's
// scroll-spy. Both are enhancements; without this file the picker closes on a
// second click and the rail is a plain list.

(function () {
  "use strict";

  function openPickers() {
    return document.querySelectorAll("details.versions[open]");
  }

  document.addEventListener("keydown", function (e) {
    if (e.key !== "Escape") return;
    openPickers().forEach(function (d) {
      d.open = false;
      d.querySelector("summary").focus();
    });
  });

  document.addEventListener("click", function (e) {
    openPickers().forEach(function (d) {
      if (!d.contains(e.target)) d.open = false;
    });
  });

  var rail = document.querySelector(".toc-rail");
  if (!rail) return;
  var links = Array.prototype.slice.call(rail.querySelectorAll('a[href^="#"]'));
  var targets = links.map(function (a) {
    return document.getElementById(decodeURIComponent(a.hash.slice(1)));
  });
  var current = -1;
  var queued = false;

  function update() {
    queued = false;
    // The line a heading must cross is the one an anchor jump parks it on,
    // plus a little so the jump itself highlights its target.
    var line = parseFloat(getComputedStyle(document.documentElement).scrollPaddingTop) + 8 || 8;
    var at = 0;
    for (var i = 0; i < targets.length; i++) {
      if (targets[i] && targets[i].getBoundingClientRect().top <= line) at = i;
    }
    var doc = document.documentElement;
    if (window.innerHeight + window.scrollY >= doc.scrollHeight - 2) at = targets.length - 1;
    if (at === current) return;
    if (current >= 0) links[current].removeAttribute("aria-current");
    links[at].setAttribute("aria-current", "location");
    current = at;
    // Keep the highlight inside a rail taller than the window. Set directly,
    // not scrollIntoView, which would scroll the page too.
    var a = links[at];
    if (a.offsetTop < rail.scrollTop || a.offsetTop + a.offsetHeight > rail.scrollTop + rail.clientHeight) {
      rail.scrollTop = a.offsetTop - rail.clientHeight / 3;
    }
  }

  function queue() {
    if (queued) return;
    queued = true;
    requestAnimationFrame(update);
  }

  window.addEventListener("scroll", queue, { passive: true });
  window.addEventListener("resize", queue);
  update();
})();
