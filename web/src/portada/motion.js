// Duraciones y curvas de las animaciones: salen de los tokens de
// maestro.css (--dur-*, --ease), así hay una sola escala para el CSS y para
// Svelte. Con prefers-reduced-motion todo dura 0.
const reduce = matchMedia("(prefers-reduced-motion: reduce)");

function token(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

// dur devuelve la duración de un token en milisegundos.
export function dur(name) {
  if (reduce.matches) return 0;
  const v = token(name);
  return v.endsWith("ms") ? parseFloat(v) : parseFloat(v) * 1000;
}

// bezier arma la función de una curva cubic-bezier(x1, y1, x2, y2): dado el
// avance en el tiempo (0 a 1) devuelve el avance del movimiento.
function bezier(x1, y1, x2, y2) {
  const cx = 3 * x1, bx = 3 * (x2 - x1) - cx, ax = 1 - cx - bx;
  const cy = 3 * y1, by = 3 * (y2 - y1) - cy, ay = 1 - cy - by;
  const x = (t) => ((ax * t + bx) * t + cx) * t;
  const y = (t) => ((ay * t + by) * t + cy) * t;
  const dx = (t) => (3 * ax * t + 2 * bx) * t + cx;
  return (p) => {
    if (p <= 0) return 0;
    if (p >= 1) return 1;
    let t = p;
    for (let i = 0; i < 8; i++) {
      const err = x(t) - p;
      if (Math.abs(err) < 1e-5) break;
      const d = dx(t);
      if (Math.abs(d) < 1e-6) break;
      t -= err / d;
    }
    // Si Newton no converge, bisección.
    if (Math.abs(x(t) - p) > 1e-4) {
      let lo = 0, hi = 1;
      t = p;
      for (let i = 0; i < 30; i++) {
        if (x(t) < p) lo = t; else hi = t;
        t = (lo + hi) / 2;
      }
    }
    return y(t);
  };
}

let cached;
// ease es la curva estándar (--ease).
export function ease(p) {
  if (!cached) {
    const m = token("--ease").match(/cubic-bezier\(([^)]+)\)/);
    const n = m ? m[1].split(",").map(Number) : [0.2, 0, 0, 1];
    cached = bezier(n[0], n[1], n[2], n[3]);
  }
  return cached(p);
}
