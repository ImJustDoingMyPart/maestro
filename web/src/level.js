// Detección del nivel de una línea de log con las formas más comunes:
// palabras en mayúsculas (ERROR, WARN), logfmt (level=error), JSON
// ("level":"error"), corchetes ([error]) y los niveles numéricos de pino
// (50 = error, 40 = warn). No es exacto: es una ayuda para filtrar.

export const INFO = 0;
export const WARN = 1;
export const ERROR = 2;

// Las palabras sueltas solo cuentan en mayúsculas: "0 errors" o "error
// handling" en minúsculas suelen ser texto, no el nivel.
const ERROR_WORD = /\b(?:ERROR|ERR|FATAL|CRIT|CRITICAL|PANIC|EMERG|ALERT)\b/;
const WARN_WORD = /\b(?:WARN|WARNING)\b/;

// level=error, lvl=eror (Grafana), "level":"error", severity: "ERROR", [error]
const KV = /(?:\b(?:level|lvl|severity|loglevel)"?\s*[=:]\s*"?|\[)(error|eror|err|fatal|crit|critical|panic|emerg|alert|warn|warning)\b/i;

// pino: "level":50
const PINO = /"level"\s*:\s*(\d{2})\b/;

// ANSI: colores y movimientos del cursor que escriben muchas imágenes.
const ANSI = /\x1b\[[0-9;?]*[ -/]*[@-~]/g;

export function stripAnsi(s) {
  return s.includes("\x1b") ? s.replace(ANSI, "") : s;
}

export function detectLevel(s) {
  const kv = KV.exec(s);
  if (kv) {
    return kv[1].toLowerCase().startsWith("warn") ? WARN : ERROR;
  }
  const pino = PINO.exec(s);
  if (pino) {
    const n = Number(pino[1]);
    return n >= 50 ? ERROR : n >= 40 ? WARN : INFO;
  }
  if (ERROR_WORD.test(s)) return ERROR;
  if (WARN_WORD.test(s)) return WARN;
  return INFO;
}
