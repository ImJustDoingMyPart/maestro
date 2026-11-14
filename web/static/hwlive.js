// Hardware en vivo: cada 15 segundos (lo que tarda el hub en consultar a
// los agents) pide la misma página y cambia solo lo que se mueve: las
// tarjetas, el encabezado del host, los puntos de las pestañas y la tabla
// de lo que más memoria usa. Los gráficos se refrescan solos (charts.js) y
// no se tocan. Si cambió la forma de la página (apareció una tarjeta, una
// sección, el agent empezó a mandar métricas), se recarga entera.
//
// Se frena mientras se edita y con la pestaña del navegador escondida; al
// volver, refresca enseguida.
(function () {
  "use strict";
  var EVERY = 15000;
  var host = document.querySelector("[data-hw-host]");
  if (!host) return;

  function paused() {
    return document.hidden || document.body.classList.contains("editing") || document.querySelector("dialog[open]");
  }

  // shape es la forma de la página: qué secciones y qué tarjetas hay.
  function shape(root) {
    var items = [];
    root.querySelectorAll("[data-hw-host] [data-hw-item]").forEach(function (el) { items.push(el.dataset.hwItem); });
    root.querySelectorAll("[data-hw-host] .empty-state").forEach(function () { items.push("empty"); });
    return items.join("\n");
  }

  function swap(sel, next) {
    var a = document.querySelector(sel), b = next.querySelector(sel);
    if (a && b && a.outerHTML !== b.outerHTML) a.replaceWith(document.importNode(b, true));
  }

  // Lo que cambió se anima (motion.js): la foto antes de tocar nada y las
  // marcas al final.
  var motion = window.maestroMotion;

  function patch(next) {
    if (shape(document) !== shape(next)) {
      location.reload();
      return;
    }
    var snap = motion && motion.snapshot();
    swap(".hw-host-head", next);
    document.querySelectorAll(".host-tab").forEach(function (tab) {
      var other = next.querySelector('.host-tab[data-host="' + CSS.escape(tab.dataset.host) + '"] .dot');
      var dot = tab.querySelector(".dot");
      if (other && dot && dot.className !== other.className) {
        var was = dot.className.replace(/\bdot\b/, "").trim();
        dot.className = other.className;
        if (motion && was) {
          dot.setAttribute("data-was", was);
          dot.addEventListener("animationend", function () { dot.removeAttribute("data-was"); }, { once: true });
        }
      }
    });
    document.querySelectorAll(".hw-tile[data-hw-item]").forEach(function (card) {
      var other = next.querySelector('.hw-tile[data-hw-item="' + CSS.escape(card.dataset.hwItem) + '"]');
      if (other && other.outerHTML !== card.outerHTML) card.replaceWith(document.importNode(other, true));
    });
    // La tabla, sin cerrar los stacks que estaban abiertos.
    var open = {};
    document.querySelectorAll(".hw-stack[open]").forEach(function (d) { open[d.dataset.stack] = true; });
    swap(".hw-table", next);
    document.querySelectorAll(".hw-stack").forEach(function (d) {
      if (!open[d.dataset.stack]) return;
      if (motion) motion.quiet(d, function () { d.open = true; });
      else d.open = true;
    });
    if (motion) motion.mark(snap);
  }

  var busy = false;
  function refresh() {
    if (busy || paused()) return;
    busy = true;
    fetch(location.pathname + location.search, { credentials: "same-origin" })
      .then(function (res) {
        // La sesión venció: el pedido terminó en la página de entrar.
        if (!res.ok || res.redirected) return null;
        return res.text();
      })
      .then(function (html) {
        if (html && !paused()) patch(new DOMParser().parseFromString(html, "text/html"));
      })
      .catch(function () { /* sin conexión: se prueba en la próxima vuelta */ })
      .finally(function () { busy = false; });
  }

  setInterval(refresh, EVERY);
  document.addEventListener("visibilitychange", function () { if (!document.hidden) refresh(); });
})();
