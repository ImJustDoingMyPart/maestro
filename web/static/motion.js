// Movimiento: marca lo que cambió entre dos vueltas para que maestro.css
// lo anime (sección "Movimiento"). La portada y la página de un servicio se
// reemplazan cada 15 segundos con htmx, y Hardware con hwlive.js: sin esto
// no hay forma de saber qué cambió, porque todo es nuevo.
//
// Cada elemento con data-watch="<clave>" se fotografía antes del cambio
// (snapshot) y se compara después (mark):
//   - si cambió su data-state, el punto y la palabra de su estado reciben
//     data-was="<estado anterior>";
//   - si no estaba, o estaba en otro grupo, recibe data-enter;
//   - si cambió su data-pulse (la hora de "Actualizado"), data-renewed;
//   - si tiene una barra de uso y cambió el ancho, la barra se desliza.
// Las marcas se borran al terminar su animación.
//
// También: quiet() hace un cambio sin transición (restaurar un stack que
// estaba abierto) y busy() pone un botón en espera mientras dura un pedido.
(function () {
  "use strict";

  function snapshot() {
    var snap = {};
    document.querySelectorAll("[data-watch]").forEach(function (el) {
      var group = el.closest("[data-group]");
      var bar = el.querySelector(".meter > span");
      snap[el.dataset.watch] = {
        state: el.dataset.state || "",
        group: group ? group.dataset.group : "",
        pulse: el.dataset.pulse || "",
        bar: bar ? bar.style.inlineSize : "",
      };
    });
    return snap;
  }

  // play pone una marca y la saca cuando termina la animación del propio
  // elemento (las de adentro también burbujean).
  function play(el, attr, value) {
    el.setAttribute(attr, value);
    function done(e) {
      if (e.target !== el) return;
      el.removeAttribute(attr);
      el.removeEventListener("animationend", done);
      el.removeEventListener("animationcancel", done);
    }
    el.addEventListener("animationend", done);
    el.addEventListener("animationcancel", done);
  }

  // changed anima el estado de el, que antes era was: su punto y su
  // palabra, sin entrar en otro elemento vigilado que tenga adentro (los
  // servicios de un stack tienen su propio estado).
  function changed(el, was) {
    var parts = [el].concat(Array.prototype.slice.call(el.querySelectorAll(".dot, .state-pill, .status")));
    parts.forEach(function (p) {
      if (!p.matches(".dot, .state-pill, .status")) return;
      if (p !== el && p.closest("[data-watch]") !== el) return;
      play(p, "data-was", was);
    });
  }

  function glide(bar, from) {
    var to = bar.style.inlineSize;
    bar.style.inlineSize = from;
    bar.getBoundingClientRect(); // fija el ancho de antes como punto de partida
    bar.style.inlineSize = to;
  }

  function mark(snap) {
    if (!snap) return;
    document.querySelectorAll("[data-watch]").forEach(function (el) {
      var was = snap[el.dataset.watch];
      var group = el.closest("[data-group]");
      if (!was || was.group !== (group ? group.dataset.group : "")) {
        // Lo de adentro de algo que entra, entra con él.
        if (!el.parentElement.closest("[data-enter]")) play(el, "data-enter", "");
        return;
      }
      if (was.state && el.dataset.state && was.state !== el.dataset.state) changed(el, was.state);
      if (was.pulse !== (el.dataset.pulse || "")) play(el, "data-renewed", "");
      var bar = el.querySelector(".meter > span");
      if (bar && was.bar && was.bar !== bar.style.inlineSize) glide(bar, was.bar);
    });
  }

  // quiet hace fn sin transiciones dentro de el: lo que cambia queda en su
  // lugar de una vez. La clase se saca en el cuadro siguiente, cuando el
  // estado nuevo ya está fijado.
  function quiet(el, fn) {
    el.classList.add("no-motion");
    try {
      fn();
    } finally {
      el.getBoundingClientRect();
      requestAnimationFrame(function () { el.classList.remove("no-motion"); });
    }
  }

  // busy pone un botón en espera: su ícono pasa a progress_activity y gira
  // (maestro.css); al terminar vuelve el de antes.
  function busy(btn, on) {
    if (!btn) return;
    var icon = btn.querySelector(":scope > i");
    if (on) {
      btn.setAttribute("aria-busy", "true");
      if (icon && !("icon" in icon.dataset)) {
        icon.dataset.icon = icon.textContent;
        icon.textContent = "progress_activity";
      }
    } else {
      btn.removeAttribute("aria-busy");
      if (icon && "icon" in icon.dataset) {
        icon.textContent = icon.dataset.icon;
        delete icon.dataset.icon;
      }
    }
  }

  // token lee un token de la escala (para lo que se anima desde JS, como
  // SortableJS); ms lo pasa a milisegundos.
  function token(name) {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }
  function ms(name) {
    if (matchMedia("(prefers-reduced-motion: reduce)").matches) return 0;
    var v = token(name);
    return v.endsWith("ms") ? parseFloat(v) : parseFloat(v) * 1000;
  }

  window.maestroMotion = { snapshot: snapshot, mark: mark, quiet: quiet, busy: busy, token: token, ms: ms };

  // htmx: la foto antes de reemplazar, las marcas después (antes de que se
  // pinte: afterSwap llega en la misma vuelta).
  var before = null;
  document.addEventListener("htmx:beforeSwap", function () { before = snapshot(); });
  document.addEventListener("htmx:afterSwap", function () {
    var snap = before;
    before = null;
    mark(snap);
  });

  // Un formulario de htmx (un diálogo, un ítem de menú) pone en espera el
  // botón que lo mandó.
  document.addEventListener("htmx:beforeRequest", function (e) {
    var cfg = e.detail.requestConfig;
    var ev = cfg && cfg.triggeringEvent;
    var btn = ev && ev.submitter;
    if (!btn) return;
    busy(btn, true);
    e.detail.elt.maestroBusy = btn;
  });
  document.addEventListener("htmx:afterRequest", function (e) {
    var btn = e.detail.elt.maestroBusy;
    if (!btn) return;
    e.detail.elt.maestroBusy = null;
    busy(btn, false);
  });
})();
