// Íconos de los servicios, en todas las páginas:
//
// 1. Centrado óptico. Cada logo trae su propio margen dentro del archivo
//    (uno pegado a la izquierda, otro con aire abajo, otro que llena todo):
//    centrarlo por su caja no alcanza. Acá se dibuja el logo en un canvas,
//    se busca dónde están sus píxeles visibles y se lo corre y escala para
//    que el dibujo quede en el centro y todos ocupen lo mismo. El resultado
//    se guarda por URL en el navegador: la próxima vez se aplica antes de
//    pintar.
// 2. El diálogo "Cambiar ícono" (#icon-dialog): busca en las colecciones
//    mientras se escribe y guarda con un formulario común.
(function () {
  "use strict";

  // ---------- Centrado óptico ----------

  var FILL = 0.86; // cuánto del cuadrado ocupa el dibujo, en su lado más largo
  var MIN = 0.8, MAX = 1.6; // límites del escalado (un PNG chico no se agranda de más)
  var SIZE = 96;
  var memo = {};

  function cached(src) {
    if (memo[src]) return memo[src];
    try {
      var v = JSON.parse(localStorage.getItem("maestro.logo:" + src) || "null");
      if (v) memo[src] = v;
      return v;
    } catch (e) {
      return null;
    }
  }
  function store(src, v) {
    memo[src] = v;
    try { localStorage.setItem("maestro.logo:" + src, JSON.stringify(v)); } catch (e) { /* sin almacenamiento */ }
  }

  // measure devuelve {dx, dy, k}: cuánto correr (en fracción del cuadrado)
  // y escalar para que el dibujo quede centrado y ocupe FILL.
  function measure(img) {
    var w = img.naturalWidth || SIZE, h = img.naturalHeight || SIZE;
    var s = Math.min(SIZE / w, SIZE / h); // object-fit: contain
    var dw = w * s, dh = h * s, ox = (SIZE - dw) / 2, oy = (SIZE - dh) / 2;
    var canvas = document.createElement("canvas");
    canvas.width = canvas.height = SIZE;
    var ctx = canvas.getContext("2d", { willReadFrequently: true });
    ctx.drawImage(img, ox, oy, dw, dh);
    var data = ctx.getImageData(0, 0, SIZE, SIZE).data; // tira si el logo es de otro origen
    var minX = SIZE, minY = SIZE, maxX = -1, maxY = -1;
    for (var y = 0; y < SIZE; y++) {
      for (var x = 0; x < SIZE; x++) {
        if (data[(y * SIZE + x) * 4 + 3] > 24) {
          if (x < minX) minX = x;
          if (x > maxX) maxX = x;
          if (y < minY) minY = y;
          if (y > maxY) maxY = y;
        }
      }
    }
    if (maxX < 0) return null;
    var cx = (minX + maxX + 1) / 2 / SIZE, cy = (minY + maxY + 1) / 2 / SIZE;
    var side = Math.max(maxX - minX + 1, maxY - minY + 1) / SIZE;
    var k = Math.min(MAX, Math.max(MIN, FILL / side));
    return { dx: (0.5 - cx) * k, dy: (0.5 - cy) * k, k: k };
  }

  function apply(img, v) {
    if (!v) return;
    img.style.transform = "translate(" + (v.dx * 100).toFixed(2) + "%, " + (v.dy * 100).toFixed(2) + "%) scale(" + v.k.toFixed(3) + ")";
  }

  function fix(img) {
    var src = img.currentSrc || img.src;
    if (!src || img.classList.contains("mono")) return;
    var v = cached(src);
    if (v) return apply(img, v);
    if (!img.complete || !img.naturalWidth && !/\.svg(\?|$)/.test(src)) return; // espera al load
    try {
      v = measure(img);
    } catch (e) {
      return; // otro origen (una URL declarada): queda como viene
    }
    if (v) {
      store(src, v);
      apply(img, v);
    }
  }

  function fixAll(root) {
    (root || document).querySelectorAll(".svc-icon img").forEach(fix);
  }
  // load no burbujea: se escucha en captura.
  document.addEventListener("load", function (e) {
    if (e.target.tagName === "IMG" && e.target.closest(".svc-icon")) fix(e.target);
  }, true);
  document.addEventListener("DOMContentLoaded", function () { fixAll(); });
  document.addEventListener("htmx:afterSettle", function (e) { fixAll(e.target); });
  // Con el tema cambia la variante (otra URL).
  matchMedia("(prefers-color-scheme: dark)").addEventListener("change", function () {
    setTimeout(fixAll, 100);
  });

  // ---------- Diálogo "Cambiar ícono" ----------

  function dialog() { return document.getElementById("icon-dialog"); }

  function choice(hit, current) {
    var b = document.createElement("button");
    b.type = "button";
    b.className = "icon-choice" + (hit.spec === current ? " selected" : "");
    b.dataset.spec = hit.spec;
    b.title = hit.title + " (" + hit.spec + ")";
    var box = document.createElement("span");
    box.className = "svc-icon";
    var img = document.createElement("img");
    img.alt = "";
    img.loading = "lazy";
    img.src = hit.src;
    if (hit.srcDark) {
      var pic = document.createElement("picture");
      var source = document.createElement("source");
      source.media = "(prefers-color-scheme: dark)";
      source.srcset = hit.srcDark;
      pic.append(source, img);
      box.append(pic);
    } else {
      box.append(img);
    }
    var label = document.createElement("span");
    label.className = "icon-choice-label";
    label.textContent = hit.title;
    b.append(box, label);
    return b;
  }

  var timer = 0, seq = 0;
  function search(q) {
    var d = dialog();
    var box = d.querySelector("[data-icon-results]");
    var input = d.querySelector("input[name=icon]");
    var mine = ++seq;
    if (!q.trim()) {
      box.replaceChildren();
      return;
    }
    fetch("/icons/search?q=" + encodeURIComponent(q), { credentials: "same-origin" })
      .then(function (r) { return r.ok ? r.json() : []; })
      .then(function (hits) {
        if (mine !== seq) return; // llegó una búsqueda más nueva
        box.replaceChildren.apply(box, hits.map(function (h) { return choice(h, input.value.trim()); }));
        if (!hits.length) {
          var p = document.createElement("p");
          p.className = "icon-empty";
          p.textContent = "Nada con ese nombre en las colecciones. Se guarda igual tal como está escrito.";
          box.append(p);
        }
      })
      .catch(function () {});
  }

  document.addEventListener("click", function (e) {
    var d = dialog();
    if (!d) return;
    var open = e.target.closest("[data-icon-pick]");
    if (open && !open.disabled) {
      e.preventDefault();
      // El menú de tres puntos de la portada se cierra al abrir el diálogo.
      var menu = open.closest("details");
      if (menu) menu.open = false;
      var form = d.querySelector("form");
      form.elements.key.value = open.dataset.key;
      form.elements.back.value = location.pathname + location.search;
      form.elements.icon.value = open.dataset.icon || "";
      d.querySelector("[data-icon-name]").textContent = open.dataset.name || "";
      d.showModal();
      requestAnimationFrame(function () { form.elements.icon.focus(); });
      // Sin ícono elegido, busca por el nombre sin ".service" ni ".timer".
      search(open.dataset.icon || (open.dataset.name || "").replace(/\.(service|timer)$/, ""));
      return;
    }
    if (!d.open) return;
    var pick = e.target.closest(".icon-choice");
    if (pick) {
      var input = d.querySelector("input[name=icon]");
      input.value = pick.dataset.spec;
      d.querySelectorAll(".icon-choice.selected").forEach(function (x) { x.classList.remove("selected"); });
      pick.classList.add("selected");
      return;
    }
    if (e.target.closest("[data-icon-auto]")) {
      var f = d.querySelector("form");
      f.elements.icon.value = "";
      f.submit();
      return;
    }
    if (e.target.closest("#icon-dialog [data-close]")) d.close();
  });

  document.addEventListener("input", function (e) {
    if (e.target.id !== "icon-input") return;
    clearTimeout(timer);
    timer = setTimeout(function () { search(e.target.value); }, 150);
  });
})();
