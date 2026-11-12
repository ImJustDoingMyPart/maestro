// Muestrario: elegir claro u oscuro sin cambiar el del sistema. ui() es de
// Beer CSS (beer.min.js, que carga como módulo: puede llegar después).
document.addEventListener("click", (e) => {
  const b = e.target.closest(".mu-modes [data-mode]");
  if (!b || typeof ui !== "function") return;
  ui("mode", b.dataset.mode);
  for (const o of b.parentElement.children) o.classList.toggle("active", o === b);
});
