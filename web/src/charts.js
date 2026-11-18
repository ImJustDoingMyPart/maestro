// Gráficos de series de tiempo con uPlot, para Hardware y la pestaña
// Métricas de un servicio.
//
// Cada gráfico es un elemento con data-chart y:
//   data-series  JSON con [{key, label}], las series de /data/metrics
//   data-unit    pct, cores (porcentaje de un núcleo), bytes, rate
//                (bytes/s), temp o plain
//   data-stack   si está, las series se dibujan apiladas (suma)
// El rango (1h, 24h, …) es uno para todos los gráficos: lo cambia
// setRange y se recuerda en el navegador.
import uPlot from "uplot";

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

// Paleta categórica de ocho colores, en orden fijo (la serie 1 siempre es
// la misma), validada para daltonismo contra superficies clara y oscura: el
// tema oscuro tiene sus propios tonos, no una inversión. Una serie nunca
// toma un color generado: los gráficos tienen como mucho ocho.
const SERIES = {
  light: ["#2a78d6", "#eb6834", "#1baf7a", "#eda100", "#e87ba4", "#008300", "#6250d6", "#e34948"],
  dark: ["#3987e5", "#d95926", "#199e70", "#c98500", "#d55181", "#008300", "#9085e9", "#e66767"],
};

function isDark() {
  if (document.body.classList.contains("dark")) return true;
  if (document.body.classList.contains("light")) return false;
  return matchMedia("(prefers-color-scheme: dark)").matches;
}

// Ejes y grilla con los colores del tema (variables de Beer CSS).
function palette() {
  const cs = getComputedStyle(document.body);
  const v = (name) => cs.getPropertyValue(name).trim();
  return {
    series: isDark() ? SERIES.dark : SERIES.light,
    axis: v("--on-surface-variant"),
    grid: v("--surface-container-highest"),
    font: '500 11px "Google Sans Flex", system-ui, sans-serif',
  };
}

function alpha(hex, a) {
  const h = hex.replace("#", "");
  const n = parseInt(h.length === 3 ? h.replace(/./g, "$&$&") : h, 16);
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${a})`;
}

// Horas del eje x según el rango: horas y minutos hasta dos días, día y mes
// hasta un par de meses, y si no, el mes.
function timeFormatter(span) {
  const opts =
    span <= 2 * 86400
      ? { hour: "2-digit", minute: "2-digit", hour12: false }
      : span <= 62 * 86400
        ? { day: "numeric", month: "short" }
        : { month: "short", year: "2-digit" };
  const f = new Intl.DateTimeFormat("es-AR", opts);
  return (t) => f.format(new Date(t * 1000));
}
const cursorTime = new Intl.DateTimeFormat("es-AR", { dateStyle: "short", timeStyle: "short", hour12: false });

// Ancho del eje y según la etiqueta más larga (ejemplo "axis autosize" de
// uPlot): "97,7 KB/s" no se corta.
function axisSize(self, values, axisIdx, cycleNum) {
  const axis = self.axes[axisIdx];
  if (cycleNum > 1) return axis._size;
  let size = axis.ticks.size + axis.gap;
  const longest = (values ?? []).reduce((acc, v) => (String(v).length > acc.length ? String(v) : acc), "");
  if (longest !== "") {
    self.ctx.font = axis.font[0];
    size += self.ctx.measureText(longest).width / devicePixelRatio;
  }
  return Math.ceil(size);
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
  if (!charts.has(el)) return; // se desmontó mientras esperaba
  draw(el, spec, data);
}

function lastValue(col) {
  for (let i = col.length - 1; i >= 0; i--) if (col[i] != null) return col[i];
  return null;
}

// Leyenda propia, en HTML: un punto del color, el nombre y el valor. Sin
// el cursor encima muestra el último valor; con el cursor, el de ese
// momento (y la hora va arriba a la derecha).
function buildLegend(el, spec, colors) {
  const box = el.querySelector("[data-chart-legend]");
  box.replaceChildren();
  box.classList.toggle("single", spec.length === 1);
  return spec.map((s, i) => {
    const item = document.createElement("span");
    item.className = "lg-item";
    const sw = document.createElement("span");
    sw.className = "lg-swatch";
    sw.style.background = colors[i];
    const label = document.createElement("span");
    label.className = "lg-label";
    label.textContent = s.label;
    label.title = s.label;
    const val = document.createElement("span");
    val.className = "lg-value";
    item.append(sw, label, val);
    box.append(item);
    return val;
  });
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
  el._plot?.destroy();
  el._plot = null;
  const legendBox = el.querySelector("[data-chart-legend]");
  const now = el.querySelector("[data-chart-now]");
  if (!hasData) {
    legendBox?.replaceChildren();
    if (now) now.textContent = "";
    return;
  }

  const raw = data.series.map((s) => s.avg);
  let values = raw;
  if (stacked) {
    // Apilado: cada serie es la suma de ella y las anteriores. La leyenda
    // muestra el valor propio, sin sumar.
    const acc = new Array(data.t.length).fill(0);
    values = raw.map((col) =>
      col.map((v, i) => {
        if (v == null) return null;
        acc[i] += v;
        return acc[i];
      }),
    );
  }

  const pal = palette();
  const colors = spec.map((_, i) => pal.series[i % pal.series.length]);
  const legendValues = buildLegend(el, spec, colors);
  const showLatest = () => {
    raw.forEach((col, i) => (legendValues[i].textContent = fmt(lastValue(col))));
    if (now) now.textContent = "";
  };
  showLatest();

  const span = data.t[data.t.length - 1] - data.t[0];
  const fmtTime = timeFormatter(span);
  const single = spec.length === 1;
  const axisCommon = {
    stroke: pal.axis,
    font: pal.font,
    grid: { stroke: pal.grid, width: 1 },
    ticks: { show: false },
    gap: 6,
  };
  const opts = {
    width: Math.max(box.clientWidth, 200),
    height: 190,
    padding: [8, 4, 0, 0],
    cursor: {
      drag: { x: false, y: false },
      points: { size: 8, width: 2, stroke: (u, si) => colors[si - 1], fill: getComputedStyle(document.body).getPropertyValue("--surface-container-low").trim() },
    },
    legend: { show: false },
    scales: {
      x: { time: true },
      y: { range: (u, min, max) => [0, Math.max(max, el.dataset.unit === "pct" ? 100 : max * 1.15 || 1)] },
    },
    axes: [
      { ...axisCommon, values: (u, ticks) => ticks.map(fmtTime), space: 70 },
      { ...axisCommon, size: axisSize, values: (u, ticks) => ticks.map(fmt), space: 36 },
    ],
    series: [
      {},
      ...spec.map((s, i) => ({
        label: s.label,
        stroke: colors[i],
        width: 2,
        // Una sola serie: área con degradé. Apiladas: relleno plano suave.
        fill: stacked
          ? alpha(colors[i], 0.18)
          : single
            ? (u) => {
                const g = u.ctx.createLinearGradient(0, u.bbox.top, 0, u.bbox.top + u.bbox.height);
                g.addColorStop(0, alpha(colors[i], 0.28));
                g.addColorStop(1, alpha(colors[i], 0));
                return g;
              }
            : undefined,
        points: { show: false },
        spanGaps: false,
      })),
    ],
    hooks: {
      setCursor: [
        (u) => {
          const idx = u.cursor.idx;
          if (idx == null) return showLatest();
          raw.forEach((col, i) => (legendValues[i].textContent = fmt(col[idx])));
          if (now) now.textContent = cursorTime.format(new Date(data.t[idx] * 1000));
        },
      ],
    },
  };
  el._plot = new uPlot(opts, [data.t, ...values], box);
}

// Los gráficos montados (mountChart) y quién escucha el rango.
const charts = new Set();
const rangeListeners = new Set();
let timer = 0;

function loadAll() {
  for (const el of charts) load(el);
}

// getRange es el rango elegido (1h, 6h, 24h, 7d, 30d, 1y).
export function getRange() {
  return range;
}

// setRange cambia el rango de todos los gráficos y lo recuerda.
export function setRange(r) {
  range = r;
  try {
    localStorage.setItem(RANGE_KEY, range);
  } catch {}
  rangeListeners.forEach((f) => f(range));
  loadAll();
}

// onRange avisa cada cambio de rango; devuelve con qué dejar de escuchar.
export function onRange(f) {
  rangeListeners.add(f);
  return () => rangeListeners.delete(f);
}

// Al cambiar el ancho (girar el celular, achicar la ventana) se ajusta el
// tamaño sin volver a pedir los datos.
const resize = new ResizeObserver((entries) => {
  for (const e of entries) {
    const el = e.target.closest("[data-chart]");
    if (el?._plot) el._plot.setSize({ width: Math.max(e.contentRect.width, 200), height: 190 });
  }
});

// El tema (claro u oscuro) cambia los colores: se redibuja.
matchMedia("(prefers-color-scheme: dark)").addEventListener("change", loadAll);

// mountChart dibuja el gráfico de el (un elemento con data-chart, ver
// arriba) y lo refresca cada minuto hasta destroy. Sirve de action de
// Svelte: update vuelve a pedir los datos (cambiaron las series).
export function mountChart(el) {
  charts.add(el);
  const plot = el.querySelector(".chart-plot");
  resize.observe(plot);
  // Los ejes se miden con la fuente de la página: se espera a que cargue.
  (document.fonts?.ready ?? Promise.resolve()).then(() => charts.has(el) && load(el));
  if (!timer) {
    timer = setInterval(() => {
      if (!document.hidden) loadAll();
    }, REFRESH_MS);
  }
  return {
    update() {
      load(el);
    },
    destroy() {
      charts.delete(el);
      resize.unobserve(plot);
      el._plot?.destroy();
      el._plot = null;
      if (!charts.size) {
        clearInterval(timer);
        timer = 0;
      }
    },
  };
}
