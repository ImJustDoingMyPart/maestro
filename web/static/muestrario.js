// Muestrario: elegir claro u oscuro sin cambiar el del sistema. ui() es de
// Beer CSS (beer.min.js, que carga como módulo: puede llegar después).
document.addEventListener("click", (e) => {
  const b = e.target.closest(".mu-modes [data-mode]");
  if (!b || typeof ui !== "function") return;
  ui("mode", b.dataset.mode);
  for (const o of b.parentElement.children) o.classList.toggle("active", o === b);
});

// Cajas de problemas del editor de Compose (partial "issuebox"): las llena
// compose.js; acá, con datos inventados, como las dejaría.
function muIssues(cls, state, icon, rows) {
  const box = document.querySelector("." + cls);
  if (!box) return;
  box.classList.add(state);
  box.hidden = false;
  box.querySelector(".issues-icon").textContent = icon;
  for (const [line, msg, raw] of rows) {
    const row = document.createElement("div");
    row.className = "issue";
    const at = document.createElement("a");
    at.className = "issue-line";
    at.href = "#";
    at.textContent = `Línea ${line}`;
    row.append(at, " ", msg);
    if (raw) {
      const r = document.createElement("div");
      r.className = "issue-raw";
      r.textContent = raw;
      row.append(r);
    }
    box.querySelector(".issues-list").append(row);
  }
}
muIssues("mu-issues-down", "down", "error", [
  [4, "«image» está repetida (también en la línea 3).", 'compose.yml: yaml: construct errors: line 1: line 4: mapping key "image" already defined at line 3'],
]);
muIssues("mu-issues-degraded", "degraded", "warning", [
  [7, "«def» no está en el .env ni en el entorno, así que queda vacía. Si el $ es parte del valor (una contraseña, por ejemplo), escribilo $$.", 'The "def" variable is not set. Defaulting to a blank string.'],
]);
