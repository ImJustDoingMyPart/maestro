// Portada: tarjetas de stack desplegadas, menú de los tres puntos y el
// diálogo de "Cambiar nombre".
(function () {
  var KEY = "maestro.stacks.abiertos";
  function load() {
    try { return JSON.parse(sessionStorage.getItem(KEY) || "[]"); } catch (e) { return []; }
  }
  function save(list) {
    try { sessionStorage.setItem(KEY, JSON.stringify(list)); } catch (e) { /* sin almacenamiento: se cierran al refrescar */ }
  }

  // htmx reemplaza la grilla cada 15 s y un <details> nuevo viene cerrado:
  // se vuelven a abrir los stacks que estaban desplegados.
  function restore(root) {
    var open = load();
    root.querySelectorAll("details.stack-details[data-key]").forEach(function (d) {
      if (open.indexOf(d.dataset.key) >= 0) d.open = true;
    });
  }

  function closeMenus(except) {
    document.querySelectorAll("details.card-menu[open]").forEach(function (d) {
      if (d !== except) d.open = false;
    });
  }

  // toggle no burbujea: se escucha en captura.
  document.addEventListener("toggle", function (e) {
    var d = e.target;
    if (!(d instanceof HTMLDetailsElement)) return;
    if (d.classList.contains("card-menu")) {
      if (d.open) closeMenus(d); // uno abierto a la vez
      return;
    }
    if (!d.dataset.key) return;
    var open = load().filter(function (k) { return k !== d.dataset.key; });
    if (d.open) open.push(d.dataset.key);
    save(open);
  }, true);

  // Tocar afuera cierra el menú.
  document.addEventListener("click", function (e) {
    if (!e.target.closest("details.card-menu")) closeMenus(null);
  });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") closeMenus(null);
  });

  // Cambiar nombre: el diálogo está fuera de la grilla.
  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-rename-key]");
    if (!btn) return;
    var dlg = document.querySelector("dialog.rename-dialog");
    if (!dlg) return;
    closeMenus(null);
    dlg.querySelector("input[name=key]").value = btn.dataset.renameKey;
    var input = dlg.querySelector("input[name=name]");
    input.value = btn.dataset.renameName;
    dlg.showModal();
    // Después del showModal, que mueve el foco por su cuenta.
    requestAnimationFrame(function () { input.focus(); input.select(); });
  });
  document.addEventListener("click", function (e) {
    if (e.target.closest("dialog.rename-dialog [data-close]")) e.target.closest("dialog").close();
  });
  document.addEventListener("htmx:afterRequest", function (e) {
    var dlg = e.target.closest && e.target.closest("dialog.rename-dialog");
    if (dlg && e.detail.successful) dlg.close();
  });

  // Mientras hay un menú o el diálogo abiertos, el refresco de la grilla
  // espera: si no, se cierran en la mano.
  document.addEventListener("htmx:beforeRequest", function (e) {
    var cfg = e.detail.requestConfig;
    if (!cfg || cfg.verb !== "get" || e.detail.elt.id !== "services") return;
    if (document.querySelector("details.card-menu[open], dialog.rename-dialog[open]")) e.preventDefault();
  });

  document.addEventListener("DOMContentLoaded", function () { restore(document); });
  document.addEventListener("htmx:afterSettle", function (e) { restore(e.target); });
})();
