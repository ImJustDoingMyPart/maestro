// Portada: recuerda qué tarjetas de stack están desplegadas. htmx reemplaza
// la grilla cada 15 s y un <details> nuevo viene cerrado; esto lo vuelve a
// abrir. Se guarda en sessionStorage: dura lo que la pestaña.
(function () {
  var KEY = "maestro.stacks.abiertos";
  function load() {
    try { return JSON.parse(sessionStorage.getItem(KEY) || "[]"); } catch (e) { return []; }
  }
  function save(list) {
    try { sessionStorage.setItem(KEY, JSON.stringify(list)); } catch (e) { /* sin almacenamiento: se cierran al refrescar */ }
  }
  function restore(root) {
    var open = load();
    root.querySelectorAll("details[data-key]").forEach(function (d) {
      if (open.indexOf(d.dataset.key) >= 0) d.open = true;
    });
  }
  // toggle no burbujea: se escucha en captura.
  document.addEventListener("toggle", function (e) {
    var d = e.target;
    if (!(d instanceof HTMLDetailsElement) || !d.dataset.key) return;
    var open = load().filter(function (k) { return k !== d.dataset.key; });
    if (d.open) open.push(d.dataset.key);
    save(open);
  }, true);
  document.addEventListener("DOMContentLoaded", function () { restore(document); });
  document.addEventListener("htmx:afterSettle", function (e) { restore(e.target); });
})();
