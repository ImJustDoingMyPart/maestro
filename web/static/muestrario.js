// Muestrario: elegir claro u oscuro sin cambiar el del sistema. ui() es de
// Beer CSS (beer.min.js, que carga como módulo: puede llegar después).
document.addEventListener("click", (e) => {
  const b = e.target.closest(".mu-modes [data-mode]");
  if (!b || typeof ui !== "function") return;
  ui("mode", b.dataset.mode);
  for (const o of b.parentElement.children) o.classList.toggle("active", o === b);
});

// Cajas de problemas del editor de Compose (partial "issuebox"): las llena
// compose.js; acá, con datos inventados, como las dejaría.
function muIssues(cls, state, icon, rows) {
  const box = document.querySelector("." + cls);
  if (!box) return;
  box.classList.add(state);
  box.hidden = false;
  box.querySelector(".issues-icon").textContent = icon;
  for (const [line, msg, raw] of rows) {
    const row = document.createElement("div");
    row.className = "issue";
    const at = document.createElement("a");
    at.className = "issue-line";
    at.href = "#";
    at.textContent = `Línea ${line}`;
    row.append(at, " ", msg);
    if (raw) {
      const r = document.createElement("div");
      r.className = "issue-raw";
      r.textContent = raw;
      row.append(r);
    }
    box.querySelector(".issues-list").append(row);
  }
}
muIssues("mu-issues-down", "down", "error", [
  [4, "«image» está repetida (también en la línea 3).", 'compose.yml: yaml: construct errors: line 1: line 4: mapping key "image" already defined at line 3'],
]);
muIssues("mu-issues-degraded", "degraded", "warning", [
  [7, "«def» no está en el .env ni en el entorno, así que queda vacía. Si el $ es parte del valor (una contraseña, por ejemplo), escribilo $$.", 'The "def" variable is not set. Defaulting to a blank string.'],
]);

// Movimiento: cada botón cambia las piezas de arriba como lo haría un
// refresco (la foto antes y las marcas después, con motion.js), así se ve
// la misma animación que en las páginas.
(function () {
  var LABELS = { up: "Arriba", degraded: "Degradado", down: "Caído", stopped: "Detenido", unknown: "Sin datos" };
  var NEXT = { up: "degraded", degraded: "down", down: "up", stopped: "up", unknown: "up" };
  var added = 0;

  // Las tarjetas del demo no tienen clave (en la portada, sí): se les pone
  // una acá para que motion.js las vigile.
  document.querySelectorAll(".svc-card:not([data-watch])").forEach(function (card, i) {
    card.dataset.watch = "card:mu-" + i;
    card.querySelectorAll(".member-row:not([data-watch])").forEach(function (row, j) {
      row.dataset.watch = "member:mu-" + i + "-" + j;
    });
  });

  // setState cambia el estado de un elemento vigilado y el de sus partes
  // (sin entrar en otro vigilado que tenga adentro).
  function setState(el, state) {
    el.dataset.state = state;
    el.querySelectorAll(".dot, .state-pill, .member-state").forEach(function (p) {
      if (p.closest("[data-watch]") !== el) return;
      if (p.classList.contains("member-state")) p.textContent = LABELS[state];
      else if (p.classList.contains("dot")) p.className = "dot " + state;
      else {
        p.className = "state-pill " + state;
        p.lastChild.textContent = LABELS[state];
      }
    });
  }

  var actions = {
    state: function () {
      document.querySelectorAll('[data-watch^="card:"], [data-watch^="member:"], [data-watch="hw:mu-sda"]').forEach(function (el) {
        setState(el, NEXT[el.dataset.state] || "up");
      });
    },
    card: function () {
      var slot = document.querySelector(".cards > .card-slot");
      var copy = slot.cloneNode(true);
      added++;
      copy.querySelectorAll("[data-watch]").forEach(function (el) { el.dataset.watch += "-copia" + added; });
      var name = copy.querySelector(".svc-name a, .svc-name");
      if (name) name.lastChild.textContent = "Copia " + added;
      slot.parentElement.append(copy);
    },
    bars: function () {
      ["hw:mu-cpu", "hw:mu-mem"].forEach(function (k) {
        var tile = document.querySelector('[data-watch="' + k + '"]');
        var pct = Math.round(5 + Math.random() * 90);
        tile.querySelector(".meter > span").style.inlineSize = pct + "%";
        tile.classList.toggle("warn", pct >= 90);
        if (k === "hw:mu-cpu") tile.querySelector(".hw-value").firstChild.textContent = pct;
      });
    },
    updated: function () {
      var el = document.getElementById("updated");
      el.dataset.pulse = String(Date.now());
      el.textContent = "Actualizado " + new Date().toLocaleTimeString("es-AR");
    },
  };

  document.addEventListener("click", function (e) {
    var b = e.target.closest("[data-mu-motion]");
    var m = window.maestroMotion;
    if (!b || !m) return;
    if (b.dataset.muMotion === "busy") {
      m.busy(b, true);
      setTimeout(function () { m.busy(b, false); }, 2500);
      return;
    }
    var snap = m.snapshot();
    actions[b.dataset.muMotion]();
    m.mark(snap);
  });
})();
