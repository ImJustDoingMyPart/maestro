// Portada: tarjetas de stack desplegadas, menú de los tres puntos, los
// diálogos (cambiar nombre, mover a grupo, grupos) y el modo edición.
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

  // Diálogos: un botón data-dialog="<id>" abre el diálogo; cada atributo
  // data-set-<campo>="valor" del botón llena el campo name="<campo>" del
  // formulario y los elementos data-text="<campo>".
  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-dialog]");
    if (!btn || btn.disabled) return;
    var dlg = document.getElementById(btn.dataset.dialog);
    if (!dlg) return;
    closeMenus(null);
    var form = dlg.querySelector("form");
    if (form) form.reset();
    var err = dlg.querySelector(".dialog-error");
    if (err) err.textContent = "";
    Array.prototype.forEach.call(btn.attributes, function (a) {
      if (a.name.indexOf("data-set-") !== 0) return;
      var field = a.name.slice("data-set-".length);
      var input = form && form.elements.namedItem(field);
      if (input) input.value = a.value;
      dlg.querySelectorAll('[data-text="' + field + '"]').forEach(function (el) { el.textContent = a.value; });
    });
    dlg.showModal();
    var focus = dlg.querySelector("[data-focus]");
    // Después del showModal, que mueve el foco por su cuenta.
    if (focus) requestAnimationFrame(function () { focus.focus(); focus.select(); });
  });
  document.addEventListener("click", function (e) {
    if (e.target.closest("dialog.form-dialog [data-close]")) e.target.closest("dialog").close();
  });
  document.addEventListener("htmx:afterRequest", function (e) {
    var dlg = e.target.closest && e.target.closest("dialog.form-dialog");
    if (dlg && e.detail.successful) dlg.close();
  });
  // Un error (400, 409) no reemplaza la grilla: el texto va al diálogo.
  document.addEventListener("htmx:responseError", function (e) {
    var dlg = e.target.closest && e.target.closest("dialog.form-dialog");
    var err = dlg && dlg.querySelector(".dialog-error");
    if (err) err.textContent = (e.detail.xhr.responseText || "No se pudo guardar.").trim();
  });

  // Modo edición: la clase "editing" en body muestra los controles de los
  // grupos y los grupos vacíos. Mientras dura, la grilla no se refresca.
  function editing() { return document.body.classList.contains("editing"); }
  function setEditing(on) {
    document.body.classList.toggle("editing", on);
    document.querySelectorAll("[data-edit-toggle]").forEach(function (b) {
      b.setAttribute("aria-pressed", on ? "true" : "false");
    });
    if (!on && window.htmx) htmx.trigger("#services", "refresh");
    document.dispatchEvent(new CustomEvent("maestro:editing", { detail: on }));
  }
  document.addEventListener("click", function (e) {
    if (e.target.closest("[data-edit-toggle]")) setEditing(!editing());
  });
  // En el modo edición los links de las tarjetas no navegan: se arrastran.
  document.addEventListener("click", function (e) {
    if (!editing()) return;
    var a = e.target.closest("#services a.stretched, #services .stack-members a");
    if (a) e.preventDefault();
  }, true);

  // Mientras hay un menú o un diálogo abiertos, o se está editando, el
  // refresco de la grilla espera: si no, se cierran en la mano. Los cambios
  // hechos desde un diálogo (hx-post) sí pasan.
  document.addEventListener("htmx:beforeRequest", function (e) {
    var cfg = e.detail.requestConfig;
    if (!cfg || cfg.verb !== "get" || e.detail.elt.id !== "services") return;
    if (editing() || document.querySelector("details.card-menu[open], dialog.form-dialog[open]")) e.preventDefault();
  });

  document.addEventListener("DOMContentLoaded", function () { restore(document); });
  document.addEventListener("htmx:afterSettle", function (e) { restore(e.target); });
})();
