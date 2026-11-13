// Problemas de un archivo del editor de Compose: lo que dijo
// `docker compose config` (el error que frena el guardado o los avisos que
// no lo frenan) más lo que se ve sin preguntarle a nadie (tabulaciones en la
// sangría). Cada problema lleva, si se puede ubicar, la línea y el tramo del
// texto, y una explicación en castellano; el mensaje original queda en raw.
//
// Es lógica pura, sin CodeMirror: la prueban los tests de Node.

// issuesFor arma la lista de problemas del texto `text` del archivo `file`
// ("compose" o "env", llamado `base` en el disco) a partir de la respuesta
// del check: `error` es la salida de compose si rechazó el archivo, y
// `warnings` los avisos si lo aceptó.
export function issuesFor({ text, file, base, error = "", warnings = [] }) {
  const lines = splitLines(text);
  const out = local(lines, file);
  const raws = [];
  if (error) raws.push(["error", error.trim()]);
  for (const w of warnings) raws.push(["warning", w.trim()]);

  for (const [severity, full] of raws) {
    const hint = explain(full, lines);
    const spots = locate(full, lines, file, base);
    // Lo que se muestra del mensaje original: las rutas, solo con el
    // nombre del archivo (la ruta entera ya está arriba del editor).
    const raw = full.replace(/(?:\/[^/\s:]+)+\/([^/\s:]+)/g, "$1");
    if (!spots.length) {
      out.push({ severity, message: hint || firstLine(raw), raw });
      continue;
    }
    for (const s of spots) {
      // La tabulación ya la marca la revisión local, con mejor explicación.
      if (out.some((o) => o.local && o.line === s.line)) continue;
      out.push({ severity, message: hint || firstLine(raw), raw, ...s });
    }
  }
  return out.sort((a, b) => (a.line ?? 0) - (b.line ?? 0));
}

// tabsToSpaces cambia las tabulaciones del principio de cada renglón por
// dos espacios: es lo que se aplica a lo que se pega en el compose.
export function tabsToSpaces(text) {
  return text.replace(/^[ \t]+/gm, (m) => m.replace(/\t/g, "  "));
}

// splitLines devuelve los renglones con su posición en el texto.
function splitLines(text) {
  const out = [];
  let from = 0;
  for (const t of text.split("\n")) {
    out.push({ text: t, from, to: from + t.length });
    from += t.length + 1;
  }
  return out;
}

function firstLine(s) {
  return s.split("\n")[0];
}

// spot es el tramo de la línea n (desde 1) sin la sangría.
function spot(lines, n) {
  const l = lines[n - 1];
  if (!l) return null;
  const indent = l.text.length - l.text.trimStart().length;
  return { line: n, from: l.from + indent, to: l.to };
}

function local(lines, file) {
  if (file !== "compose") return [];
  const out = [];
  lines.forEach((l, i) => {
    const m = /^ *\t[ \t]*/.exec(l.text);
    if (m) {
      out.push({
        severity: "error", local: true, line: i + 1, from: l.from, to: l.from + m[0].length,
        message: "Hay una tabulación en la sangría: en YAML la sangría va solo con espacios.",
      });
    }
  });
  return out;
}

// explain traduce los mensajes de compose que más aparecen. Si no conoce el
// mensaje, null: se muestra el original.
export function explain(raw, lines = []) {
  let m;
  if ((m = /mapping key "([^"]+)" already defined at line (\d+)/.exec(raw))) {
    return `«${m[1]}» está repetida (también en la línea ${m[2]}).`;
  }
  if ((m = /additional propert(?:y|ies) '([^']+)' not allowed/.exec(raw)) || (m = /\.([^.\s]+) false schema/.exec(raw))) {
    return `«${m[1]}» no es una opción de compose en ese lugar. ¿Está bien escrita y con la sangría correcta?`;
  }
  if ((m = /The "([^"]+)" variable is not set/.exec(raw))) {
    return `«${m[1]}» no está en el .env ni en el entorno, así que queda vacía. Si el $ es parte del valor (una contraseña, por ejemplo), escribilo $$.`;
  }
  if (/attribute `version` is obsolete/.test(raw)) {
    return "«version» ya no se usa y compose la ignora: se puede borrar.";
  }
  if (/invalid interpolation format/.test(raw)) {
    return "Hay un $ que compose no entiende (¿un ${ sin cerrar?). Si el $ es parte del valor, escribilo $$.";
  }
  if ((m = /service "([^"]+)" has neither an image nor a build/.exec(raw))) {
    return `El servicio «${m[1]}» no tiene image ni build: compose no sabe qué levantar.`;
  }
  if ((m = /refers to undefined (network|volume|secret|config) (\S+?):?(?:\s|$)/.exec(raw))) {
    const kind = { network: "La red", volume: "El volumen", secret: "El secreto", config: "La config" }[m[1]];
    return `${kind} «${m[2]}» no está declarado en la sección ${m[1]}s del final.`;
  }
  if (/found character that cannot start any token/.test(raw)) {
    const n = lineNumber(raw);
    if (n && /\t/.test(lines[n - 1]?.text || "")) return "Hay una tabulación: en YAML la sangría va solo con espacios.";
    return "Hay un carácter que no puede ir ahí (¿una tabulación, o un @ o ` al principio de un valor? Ponelo entre comillas).";
  }
  if (/did not find expected key/.test(raw)) {
    return "La sangría no coincide con la de las líneas de arriba.";
  }
  if (/did not find expected '-' indicator/.test(raw)) {
    return "La sangría de la lista no coincide: cada elemento empieza con «- » a la misma altura.";
  }
  if (/mapping values are not allowed/.test(raw)) {
    return "Hay un «: » dentro de un valor: poné el valor entre comillas.";
  }
  if (/could not find expected ':'/.test(raw)) {
    return "Falta el «:» después de la clave.";
  }
  if (/key cannot contain a space/.test(raw)) {
    return "El nombre de la variable no puede tener espacios.";
  }
  if (/unexpected character .* in variable name/.test(raw)) {
    return "El nombre de la variable tiene un carácter que no va: solo letras, números y _.";
  }
  return null;
}

// lineNumber saca la línea de un mensaje del parser de YAML o del .env: "at
// L4.C20" o "at L2.C3-L4.C4" (el error está al final del tramo), o "line 4:"
// (el último, porque viene anidado: "line 1: line 4: mapping key...").
function lineNumber(raw) {
  let m = /\bat L(\d+)\.C\d+(?:-L(\d+)\.C\d+)?/.exec(raw);
  if (m) return Number(m[2] || m[1]);
  let n = 0;
  for (m of raw.matchAll(/\bline (\d+):/g)) n = Number(m[1]);
  return n;
}

// locate ubica el problema en el texto. Devuelve [] si no se puede: el
// mensaje habla de otro archivo, o no dice dónde.
function locate(raw, lines, file, base) {
  // Si nombra un archivo y no es este, el problema está en el otro (un
  // .env roto que el compose usa, por ejemplo).
  const named = /(?:failed to (?:parse|read)|validating) ([^\s:]+):/.exec(raw) || /^([^\s:]+): the attribute/.exec(raw);
  if (named && named[1].split("/").pop() !== base) return [];

  const n = lineNumber(raw);
  if (n) return [spot(lines, n)].filter(Boolean);
  if (file !== "compose") return [];

  let m;
  if ((m = /The "([^"]+)" variable is not set/.exec(raw))) return varRefs(lines, m[1]);
  if (/attribute `version`/.test(raw)) return pathSpot(lines, ["version"]);
  if ((m = /\b((?:services|networks|volumes|configs|secrets)(?:\.[^\s.:]+)+)/.exec(raw))) {
    const segs = m[1].replace(/\.$/, "").split(".");
    const extra = /additional propert(?:y|ies) '([^']+)'/.exec(raw);
    if (extra) segs.push(extra[1]);
    return pathSpot(lines, segs);
  }
  if ((m = /service "([^"]+)"/.exec(raw))) return pathSpot(lines, ["services", m[1]]);
  return [];
}

// varRefs marca cada $VAR y ${VAR} del texto que no esté escapado con $$.
// ${VAR:-x} y compañía no: con valor por defecto compose no avisa.
function varRefs(lines, name) {
  const esc = name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const re = new RegExp(`\\$+(?:\\{${esc}\\}|${esc}(?![A-Za-z0-9_]))`, "g");
  const out = [];
  lines.forEach((l, i) => {
    for (const m of l.text.matchAll(re)) {
      const dollars = /^\$+/.exec(m[0])[0].length;
      if (dollars % 2 === 0) continue; // $$VAR es un $ literal
      out.push({ line: i + 1, from: l.from + m.index + dollars - 1, to: l.from + m.index + m[0].length });
    }
  });
  return out;
}

// pathSpot busca la clave más profunda de segs (services.web.ports...) que
// exista en el YAML, mirando la sangría. Los índices de lista ([1], 0) se
// saltean: queda marcada la clave de la lista.
export function pathSpot(lines, segs) {
  segs = segs.filter((s) => !/^\[?\d+\]?$/.test(s));
  const stack = []; // claves abiertas: { indent, key }
  let best = 0, bestDepth = 0;
  lines.forEach((l, i) => {
    const m = /^(\s*(?:-\s+)?)(["']?)([^\s"'#:][^#:]*?)\2\s*:(?:\s|$)/.exec(l.text);
    if (!m) return;
    const indent = m[1].length;
    while (stack.length && stack[stack.length - 1].indent >= indent) stack.pop();
    stack.push({ indent, key: m[3] });
    if (stack.length > segs.length || stack.length <= bestDepth) return;
    if (stack.every((s, j) => s.key === segs[j])) {
      best = i + 1;
      bestDepth = stack.length;
    }
  });
  return best ? [spot(lines, best)] : [];
}
