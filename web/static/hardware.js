// Modo edición de Hardware: arrastrar tarjetas, gráficos y secciones para
// ordenarlos y el botón del ojo para ocultarlos o mostrarlos, para el host
// que se ve; y arrastrar las pestañas de los hosts (el primero es el que se
// abre al entrar). Cada cambio se guarda enseguida.
(function () {
  var host = document.querySelector("[data-hw-host]");
  if (!host) return;
  var hostName = host.dataset.hwHost;

  function send(url, body) {
    if (!body.has("host")) body.append("host", hostName);
    return fetch(url, { method: "POST", body: body, credentials: "same-origin" }).then(function (res) {
      if (!res.ok) throw new Error(res.status);
    });
  }

  function editing() { return document.body.classList.contains("editing"); }
  function setEditing(on) {
    document.body.classList.toggle("editing", on);
    document.querySelectorAll("[data-edit-toggle]").forEach(function (b) {
      b.setAttribute("aria-pressed", on ? "true" : "false");
    });
    if (on) startSorting(); else stopSorting();
  }
  // Al pasar a otro host desde su pestaña se sigue editando.
  document.addEventListener("click", function (e) {
    if (!editing() || !e.target.closest(".host-tab")) return;
    try { sessionStorage.setItem("maestro.hwEditing", "1"); } catch (err) { /* sin almacenamiento */ }
  });
  document.addEventListener("click", function (e) {
    if (e.target.closest("[data-edit-toggle]")) setEditing(!editing());
  });

  // El ojo: oculta o muestra el elemento que lo contiene. Si falla, vuelve
  // a como estaba.
  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-hw-hide]");
    if (!btn) return;
    e.preventDefault();
    var item = btn.closest("[data-hw-item]");
    if (!item) return;
    var hidden = !item.classList.contains("is-hidden");
    item.classList.toggle("is-hidden", hidden);
    btn.setAttribute("aria-pressed", hidden ? "true" : "false");
    var body = new URLSearchParams();
    body.append("item", item.dataset.hwItem);
    body.append("hidden", hidden ? "1" : "0");
    send("/hardware/hidden", body).catch(function () {
      item.classList.toggle("is-hidden", !hidden);
      btn.setAttribute("aria-pressed", hidden ? "false" : "true");
    });
  });

  // Arrastrar (SortableJS, el mismo camino en la computadora y el celular;
  // en pantallas táctiles hay que mantener apretado un momento).
  var sortables = [];
  function stopSorting() {
    sortables.forEach(function (s) { s.destroy(); });
    sortables = [];
  }
  var opts = {
    animation: 150, delay: 250, delayOnTouchOnly: true, forceFallback: true, fallbackTolerance: 4,
    filter: "[data-hw-hide], [data-icon-pick]", preventOnFilter: false,
    ghostClass: "drag-ghost", chosenClass: "drag-chosen",
  };
  function sortable(list, extra, onEnd) {
    var o = Object.assign({}, opts, extra, { onEnd: onEnd });
    sortables.push(Sortable.create(list, o));
  }
  function startSorting() {
    stopSorting();
    if (!window.Sortable) return;
    host.querySelectorAll("[data-hw-sort]").forEach(function (list) {
      // Las secciones se arrastran desde su manija; lo de adentro, de
      // cualquier parte.
      var extra = list.hasAttribute("data-hw-sections")
        ? { draggable: ".hw-section", handle: "[data-hw-handle]" }
        : { draggable: "[data-hw-item]" };
      sortable(list, extra, function () {
        var body = new URLSearchParams();
        list.querySelectorAll(":scope > [data-hw-item]").forEach(function (el) { body.append("item", el.dataset.hwItem); });
        send("/hardware/order", body).catch(function () { location.reload(); });
      });
    });
    var tabs = document.querySelector("[data-hw-hosts]");
    if (tabs) {
      sortable(tabs, { draggable: ".host-tab" }, function () {
        var body = new URLSearchParams();
        tabs.querySelectorAll(".host-tab").forEach(function (el) { body.append("host", el.dataset.host); });
        send("/hardware/hosts", body).catch(function () { location.reload(); });
      });
    }
  }

  try {
    if (sessionStorage.getItem("maestro.hwEditing")) {
      sessionStorage.removeItem("maestro.hwEditing");
      setEditing(true);
    }
  } catch (e) { /* sin almacenamiento */ }
})();
