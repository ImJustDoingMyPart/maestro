// Modo edición de Hardware: arrastrar mosaicos y gráficos para ordenarlos
// y el botón del ojo para ocultar o mostrar mosaicos, gráficos y
// secciones. Cada cambio se guarda enseguida, para el host que se ve.
(function () {
  var host = document.querySelector("[data-hw-host]");
  if (!host) return;
  var hostName = host.dataset.hwHost;

  function send(url, body) {
    body.append("host", hostName);
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
  function startSorting() {
    stopSorting();
    if (!window.Sortable) return;
    host.querySelectorAll("[data-hw-sort]").forEach(function (list) {
      sortables.push(Sortable.create(list, {
        animation: 150, delay: 250, delayOnTouchOnly: true, forceFallback: true, fallbackTolerance: 4,
        draggable: "[data-hw-item]", filter: "[data-hw-hide]", preventOnFilter: false,
        ghostClass: "drag-ghost", chosenClass: "drag-chosen",
        onEnd: function () {
          var body = new URLSearchParams();
          list.querySelectorAll(":scope > [data-hw-item]").forEach(function (el) { body.append("item", el.dataset.hwItem); });
          send("/hardware/order", body).catch(function () { location.reload(); });
        },
      }));
    });
  }
})();
