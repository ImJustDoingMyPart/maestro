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

  // htmx reemplaza la grilla cada 15 s y una tarjeta nueva viene cerrada:
  // se vuelven a abrir los stacks que estaban desplegados.
  function setOpen(card, on) {
    card.classList.toggle("open", on);
    var btn = card.querySelector("[data-stack-toggle]");
    if (btn) btn.setAttribute("aria-expanded", on ? "true" : "false");
  }
  function restore(root) {
    var open = load();
    root.querySelectorAll(".svc-card[data-stack]").forEach(function (card) {
      if (open.indexOf(card.dataset.stack) >= 0) setOpen(card, true);
    });
  }

  // Una tarjeta de stack se despliega con su botón o tocándola en
  // cualquier lugar que no sea un link, un botón o el menú.
  document.addEventListener("click", function (e) {
    var card = e.target.closest(".svc-card[data-stack]");
    if (!card || editing()) return;
    var onToggle = e.target.closest("[data-stack-toggle]");
    if (!onToggle && e.target.closest("a, button, details, .stack-members")) return;
    var on = !card.classList.contains("open");
    setOpen(card, on);
    var open = load().filter(function (k) { return k !== card.dataset.stack; });
    if (on) open.push(card.dataset.stack);
    save(open);
  });

  function closeMenus(except) {
    document.querySelectorAll("details.card-menu[open]").forEach(function (d) {
      if (d !== except) d.open = false;
    });
  }

  // toggle no burbujea: se escucha en captura.
  document.addEventListener("toggle", function (e) {
    var d = e.target;
    if (d instanceof HTMLDetailsElement && d.classList.contains("card-menu") && d.open) closeMenus(d); // uno abierto a la vez
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
    if (form) HTMLFormElement.prototype.reset.call(form); // un control name="reset" taparía form.reset
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

  // Arrastrar (SortableJS, que anda con el dedo: en pantallas táctiles hay
  // que mantener apretado un momento, así el gesto de desplazar la página
  // no arrastra). Las tarjetas se mueven dentro de su grupo y entre grupos;
  // los grupos, por su manija. Cada cambio se guarda enseguida. forceFallback
  // usa eventos de puntero en vez del drag and drop de HTML5: el mismo
  // camino en la computadora y en el celular.
  var sortables = [];
  function stopSorting() {
    sortables.forEach(function (s) { s.destroy(); });
    sortables = [];
  }
  function startSorting() {
    stopSorting();
    if (!window.Sortable) return;
    var common = { animation: 150, delay: 250, delayOnTouchOnly: true, forceFallback: true, fallbackTolerance: 4, ghostClass: "drag-ghost", chosenClass: "drag-chosen" };
    document.querySelectorAll("#services .group .cards").forEach(function (grid) {
      sortables.push(Sortable.create(grid, Object.assign({}, common, {
        group: "cards",
        draggable: "[data-key]",
        filter: ".card-menu, .svc-actions",
        preventOnFilter: false,
        onEnd: function (e) {
          if (e.from === e.to && e.oldIndex === e.newIndex) return;
          var section = e.to.closest(".group");
          section.classList.remove("empty");
          var body = new URLSearchParams();
          body.append("group", section.dataset.group);
          e.to.querySelectorAll(":scope > [data-key]").forEach(function (el) { body.append("key", el.dataset.key); });
          send("/layout/cards", body);
        },
      })));
    });
    var list = document.querySelector("#services .groups");
    if (list) {
      sortables.push(Sortable.create(list, Object.assign({}, common, {
        draggable: ".group:not(.otros)",
        handle: ".group-handle",
        onEnd: function (e) {
          if (e.oldIndex === e.newIndex) return;
          var body = new URLSearchParams();
          list.querySelectorAll(":scope > .group:not(.otros)").forEach(function (el) { body.append("name", el.dataset.group); });
          send("/layout/groups", body);
        },
      })));
    }
  }
  document.addEventListener("maestro:editing", function (e) {
    if (e.detail) startSorting(); else stopSorting();
  });

  // send guarda un cambio de orden. Si falla (por ejemplo, una tarjeta
  // con el grupo del archivo), avisa y vuelve a pedir la grilla, que
  // deshace lo arrastrado.
  var forceRefresh = false;
  function send(url, body) {
    fetch(url, { method: "POST", body: body, credentials: "same-origin" }).then(function (res) {
      if (res.ok) return;
      return res.text().then(function (t) { fail(t.trim() || "No se pudo guardar el orden."); });
    }).catch(function () { fail("No se pudo guardar el orden: sin conexión."); });
  }
  function fail(msg) {
    var bar = document.getElementById("layout-error");
    if (bar) {
      bar.textContent = msg;
      bar.classList.add("active");
      setTimeout(function () { bar.classList.remove("active"); }, 5000);
    }
    forceRefresh = true;
    if (window.htmx) htmx.trigger("#services", "refresh");
  }

  // Mientras hay un menú o un diálogo abiertos, o se está editando, el
  // refresco de la grilla espera: si no, se cierran en la mano. Los cambios
  // hechos desde un diálogo (hx-post) sí pasan.
  document.addEventListener("htmx:beforeRequest", function (e) {
    var cfg = e.detail.requestConfig;
    if (!cfg || cfg.verb !== "get" || e.detail.elt.id !== "services") return;
    if (forceRefresh) { forceRefresh = false; return; }
    if (editing() || document.querySelector("details.card-menu[open], dialog.form-dialog[open]")) e.preventDefault();
  });

  document.addEventListener("DOMContentLoaded", function () { restore(document); });
  document.addEventListener("htmx:afterSettle", function (e) {
    restore(e.target);
    if (editing()) startSorting(); // la grilla es otra: los Sortable viejos quedaron sueltos
  });
})();
