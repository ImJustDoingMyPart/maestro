// Filtro de la bandeja de Descubiertos: por texto y "solo lo que falla".
// Esconde filas y, si un host se queda sin filas visibles, el host entero.
(() => {
  const text = document.querySelector("[data-found-filter]");
  const failing = document.querySelector("[data-found-failing]");
  const empty = document.querySelector(".found-empty");
  if (!text || !failing) return;

  const apply = () => {
    const q = text.value.trim().toLowerCase();
    const onlyFailing = failing.checked;
    let any = false;
    for (const host of document.querySelectorAll(".found-host")) {
      let visible = 0;
      for (const li of host.querySelectorAll("li[data-state]")) {
        const st = li.dataset.state;
        const ok = (!q || li.dataset.text.toLowerCase().includes(q)) &&
          (!onlyFailing || st === "down" || st === "degraded");
        li.hidden = !ok;
        if (ok) visible++;
      }
      host.hidden = visible === 0;
      if (visible) any = true;
    }
    empty.hidden = any;
  };
  text.addEventListener("input", apply);
  failing.addEventListener("change", apply);
})();
