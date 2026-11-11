// Página del servicio: botón "Copiar informe". Pide el informe a la API
// (con la sesión) y lo copia al portapapeles. Si no se puede copiar (sin
// HTTPS el navegador no da el portapapeles, o lo niega), lo muestra en un
// diálogo para copiarlo a mano.
(function () {
  function fetchReport(url) {
    return fetch(url, { credentials: "same-origin" }).then(function (r) {
      if (!r.ok) throw new Error("el hub respondió " + r.status);
      return r.text();
    });
  }

  // copy empieza a escribir en el portapapeles dentro del clic: Safari
  // exige que el pedido salga del gesto, así que el texto va como promesa
  // dentro del ClipboardItem en vez de esperarlo antes.
  function copy(text) {
    if (!window.isSecureContext || !navigator.clipboard) {
      return Promise.reject(new Error("sin portapapeles"));
    }
    if (window.ClipboardItem && navigator.clipboard.write) {
      var blob = text.then(function (t) { return new Blob([t], { type: "text/plain" }); });
      return navigator.clipboard.write([new ClipboardItem({ "text/plain": blob })]);
    }
    return text.then(function (t) { return navigator.clipboard.writeText(t); });
  }

  // within corta una promesa que no termina: si el navegador deja colgado
  // el permiso del portapapeles, el botón no puede quedar deshabilitado
  // para siempre.
  function within(p, ms) {
    return Promise.race([p, new Promise(function (_, reject) {
      setTimeout(function () { reject(new Error("el portapapeles no respondió")); }, ms);
    })]);
  }

  function flash(btn, label, icon) {
    var span = btn.querySelector("span"), i = btn.querySelector("i");
    var before = [span.textContent, i.textContent];
    span.textContent = label;
    i.textContent = icon;
    setTimeout(function () { span.textContent = before[0]; i.textContent = before[1]; }, 2000);
  }

  function showDialog(text, url) {
    var dlg = document.querySelector("dialog.report-dialog");
    dlg.querySelector("textarea").value = text;
    dlg.querySelector("a[data-raw]").href = url;
    dlg.showModal();
    var ta = dlg.querySelector("textarea");
    ta.focus();
    ta.select();
  }

  document.addEventListener("click", function (e) {
    if (e.target.closest("dialog.report-dialog [data-close]")) {
      e.target.closest("dialog").close();
      return;
    }
    var btn = e.target.closest("button[data-report]");
    if (!btn || btn.disabled) return;
    var url = btn.dataset.report;
    btn.disabled = true;
    var text = fetchReport(url);
    within(copy(text), 3000)
      .then(function () { flash(btn, "Copiado", "check"); })
      .catch(function () {
        return text.then(function (t) { showDialog(t, url); }, function (err) {
          console.error("informe:", err);
          flash(btn, "No se pudo", "error");
        });
      })
      .finally(function () { btn.disabled = false; });
  });
})();
