// Horas en la zona del navegador (el hub corre en UTC).

const timeFmt = new Intl.DateTimeFormat("es-AR", { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
const dateTimeFmt = new Intl.DateTimeFormat("es-AR", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });

function valid(t) {
  if (!t || String(t).startsWith("0001-")) return null;
  const d = new Date(t);
  return isNaN(d) ? null : d;
}

// time da "15:04:05"; "" si no hay hora.
export function time(t) {
  const d = valid(t);
  return d ? timeFmt.format(d) : "";
}

// dateTime da "02/01 15:04"; "" si no hay hora.
export function dateTime(t) {
  const d = valid(t);
  return d ? dateTimeFmt.format(d) : "";
}

export const plural = (n, one, many) => n + " " + (n === 1 ? one : many);
