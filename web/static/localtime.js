// Muestra en la zona horaria del navegador las horas que manda el servidor
// como <time datetime="..." data-local="time|datetime">. El hub corre en
// un contenedor que puede estar en UTC; el texto que trae el elemento es el
// de respaldo sin JavaScript.
(function () {
  var fmt = {
    time: new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false }),
    datetime: new Intl.DateTimeFormat(undefined, { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }),
    short: new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit", hour12: false }),
    full: new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium", hour12: false }),
  };
  function localize(root) {
    root.querySelectorAll("time[data-local]").forEach(function (el) {
      var d = new Date(el.getAttribute("datetime"));
      if (isNaN(d)) return;
      el.textContent = (fmt[el.dataset.local] || fmt.time).format(d);
      if (!el.title) el.title = fmt.full.format(d);
    });
    // Barras del historial de checks: el título lleva su intervalo.
    root.querySelectorAll("[data-from][data-to]").forEach(function (el) {
      var a = new Date(el.dataset.from), b = new Date(el.dataset.to);
      if (isNaN(a) || isNaN(b)) return;
      el.title = fmt.short.format(a) + "–" + fmt.short.format(b) + ": " + el.dataset.summary;
    });
  }
  document.addEventListener("DOMContentLoaded", function () { localize(document); });
  // Lo que htmx reemplaza (la grilla cada 15 s, el historial de checks).
  document.addEventListener("htmx:afterSettle", function (e) { localize(e.target); });
})();
