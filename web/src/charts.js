// Gráficos de series de tiempo con uPlot, para Hardware y la pestaña
// Métricas de un servicio.
//
// Cada gráfico es un elemento con data-chart y:
//   data-series  JSON con [{key, label}], las series de /data/metrics
//   data-unit    pct, cores (porcentaje de un núcleo), bytes, rate
//                (bytes/s), temp o plain
//   data-stack   si está, las series se dibujan apiladas (suma)
// Un elemento con data-ranges tiene botones con data-range (1h, 24h, …):
// el elegido se recuerda en el navegador y cambia todos los gráficos.
import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";

const RANGE_KEY = "maestro.chartRange";
const REFRESH_MS = 60_000;

let range = "24h";
try {
  range = localStorage.getItem(RANGE_KEY) || range;
} catch {
  // sin almacenamiento (ventana privada): queda el de siempre
}

const nf = (digits) => new Intl.NumberFormat("es-AR", { maximumFractionDigits: digits });
const n0 = nf(0), n1 = nf(1), n2 = nf(2);

function bytes(v) {
  if (v == null) return "–";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (Math.abs(v) >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return (i === 0 || Math.abs(v) >= 100 ? n0 : n1).format(v) + " " + units[i];
}

const FORMAT = {
  pct: (v) => (v == null ? "–" : n1.format(v) + " %"),
  cores: (v) => (v == null ? "–" : n1.format(v) + " %"),
  bytes,
  rate: (v) => (v == null ? "–" : bytes(v) + "/s"),
  temp: (v) => (v == null ? "–" : n0.format(v) + " °C"),
  plain: (v) => (v == null ? "–" : n2.format(v)),
};

// Colores de la paleta de Beer CSS (Material 3), leídos de las variables
// del documento: siguen al tema claro u oscuro.
function palette() {
  const cs = getComputedStyle(document.body);
  const v = (name) => cs.getPropertyValue(name).trim();
  return {
    series: [v("--primary"), v("--tertiary"), v("--secondary"), v("--error"), "#8e7cc3", "#e69138", "#6aa84f", "#3d85c6"],
    axis: v("--on-surface-variant"),
    grid: v("--outline-variant"),
  };
}

function seriesSpec(el) {
  try {
    return JSON.parse(el.dataset.series || "[]");
  } catch {
    return [];
  }
}

async function load(el) {
  const spec = seriesSpec(el);
  if (!spec.length) return;
  const qs = new URLSearchParams({ range });
  for (const s of spec) qs.append("key", s.key);
  let data;
  try {
    const res = await fetch("/data/metrics?" + qs, { headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error(res.status);
    data = await res.json();
  } catch (err) {
    el.dataset.state = "error";
    el.querySelector(".chart-msg")?.replaceChildren("No se pudieron leer los datos.");
    return;
  }
  draw(el, spec, data);
}

function draw(el, spec, data) {
  const fmt = FORMAT[el.dataset.unit] || FORMAT.plain;
  const stacked = "stack" in el.dataset;
  const hasData = data.t.length > 0 && data.series.some((s) => s.avg.some((v) => v != null));
  el.dataset.state = hasData ? "ok" : "empty";
  const msg = el.querySelector(".chart-msg");
  if (msg) msg.textContent = hasData ? "" : "Sin datos en este rango todavía.";

  const box = el.querySelector(".chart-plot");
  box.replaceChildren();
  if (!hasData) return;

  let values = data.series.map((s) => s.avg);
  if (stacked) {
    // Apilado: cada serie es la suma de ella y las anteriores. El valor
    // propio se muestra en la leyenda con el valor sin sumar.
    const acc = new Array(data.t.length).fill(0);
    values = values.map((col) =>
      col.map((v, i) => {
        if (v == null) return null;
        acc[i] += v;
        return acc[i];
      }),
    );
  }

  const pal = palette();
  const raw = data.series.map((s) => s.avg);
  const opts = {
    width: Math.max(box.clientWidth, 200),
    height: 180,
    cursor: { drag: { x: false, y: false } },
    legend: { live: true },
    scales: { x: { time: true }, y: { range: (u, min, max) => [0, Math.max(max, el.dataset.unit === "pct" ? 100 : max * 1.1 || 1)] } },
    axes: [
      { stroke: pal.axis, grid: { stroke: pal.grid, width: 1 }, ticks: { stroke: pal.grid } },
      { stroke: pal.axis, grid: { stroke: pal.grid, width: 1 }, ticks: { stroke: pal.grid }, size: 70, values: (u, ticks) => ticks.map(fmt) },
    ],
    series: [
      { value: (u, v) => (v == null ? "–" : new Date(v * 1000).toLocaleString("es-AR", { dateStyle: "short", timeStyle: "short" })) },
      ...spec.map((s, i) => ({
        label: s.label,
        stroke: pal.series[i % pal.series.length],
        width: 1.5,
        fill: stacked ? pal.series[i % pal.series.length] + "33" : undefined,
        points: { show: false },
        spanGaps: false,
        value: (u, v, si, idx) => fmt(idx == null ? null : raw[si - 1][idx]),
      })),
    ],
  };
  const plot = new uPlot(opts, [data.t, ...values], box);
  el._plot = plot;
}

function loadAll() {
  for (const el of document.querySelectorAll("[data-chart]")) load(el);
}

function setupRanges() {
  for (const nav of document.querySelectorAll("[data-ranges]")) {
    const buttons = nav.querySelectorAll("[data-range]");
    const mark = () => buttons.forEach((b) => b.classList.toggle("active", b.dataset.range === range));
    mark();
    buttons.forEach((b) =>
      b.addEventListener("click", () => {
        range = b.dataset.range;
        try {
          localStorage.setItem(RANGE_KEY, range);
        } catch {}
        document.querySelectorAll("[data-ranges]").forEach((n) =>
          n.querySelectorAll("[data-range]").forEach((x) => x.classList.toggle("active", x.dataset.range === range)),
        );
        loadAll();
      }),
    );
  }
}

// Al cambiar el ancho (girar el celular, achicar la ventana) se ajusta el
// tamaño sin volver a pedir los datos.
const resize = new ResizeObserver((entries) => {
  for (const e of entries) {
    const el = e.target.closest("[data-chart]");
    if (el?._plot) el._plot.setSize({ width: Math.max(e.contentRect.width, 200), height: 180 });
  }
});

function init() {
  setupRanges();
  for (const el of document.querySelectorAll("[data-chart] .chart-plot")) resize.observe(el);
  loadAll();
  setInterval(() => {
    if (!document.hidden) loadAll();
  }, REFRESH_MS);
  // El tema (claro u oscuro) cambia los colores: se redibuja.
  matchMedia("(prefers-color-scheme: dark)").addEventListener("change", loadAll);
}

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init);
else init();
