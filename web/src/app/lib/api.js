// Pedidos al hub.
//
// Las páginas de la aplicación están en la misma URL como HTML (el shell) y
// como JSON (sus datos): getPage pide el JSON con Accept. Las acciones usan
// los mismos formularios de siempre (POST con campos): el hub responde con
// una redirección, que acá no se sigue y cuenta como que salió bien; un
// error trae el texto en el cuerpo (400, 409) o, en los formularios de la
// aplicación, JSON.

// loginGone manda al login si la sesión venció (el hub redirige a /login).
function loginGone(res) {
  if (res.redirected && new URL(res.url).pathname === "/login") {
    location.href = "/login";
    return true;
  }
  return false;
}

// getPage pide los datos de una página. Devuelve {status, data}; data es
// null si la respuesta no fue JSON.
export async function getPage(url, signal) {
  const res = await fetch(url, { headers: { Accept: "application/json" }, credentials: "same-origin", signal });
  if (loginGone(res)) throw new Error("sesión vencida");
  const type = res.headers.get("Content-Type") || "";
  if (!type.includes("application/json")) {
    if (!res.ok) throw new Error("El hub respondió " + res.status + ".");
    throw new Error("El hub no respondió con datos: ¿la sesión venció?");
  }
  return { status: res.status, data: await res.json() };
}

// getJSON pide un JSON cualquiera (la portada, la búsqueda de íconos).
export async function getJSON(url, signal) {
  const res = await fetch(url, { credentials: "same-origin", signal });
  if (loginGone(res)) throw new Error("sesión vencida");
  if (!res.ok) throw new Error("El hub respondió " + res.status + ".");
  return res.json();
}

// getPortada se queda por compatibilidad con la portada.
export function getPortada() {
  return getJSON("/api/portada").catch((e) => {
    throw new Error(e.message === "sesión vencida" ? e.message : "No se pudo leer la portada.");
  });
}

function formBody(fields) {
  const body = new URLSearchParams();
  for (const [k, v] of Object.entries(fields)) {
    if (Array.isArray(v)) v.forEach((x) => body.append(k, x));
    else if (v !== undefined && v !== null) body.append(k, v);
  }
  return body;
}

// post manda un formulario. Devuelve "" si salió bien o el texto del error.
export async function post(url, fields) {
  const r = await submit(url, fields);
  return r.ok ? "" : r.error;
}

// submit manda un formulario y devuelve {ok, status, data, error}: data es
// el JSON de la respuesta si lo hubo (un formulario con errores de
// validación vuelve con sus datos y los errores).
export async function submit(url, fields) {
  let res;
  try {
    res = await fetch(url, {
      method: "POST",
      body: fields instanceof FormData ? new URLSearchParams(fields) : formBody(fields),
      headers: { Accept: "application/json" },
      credentials: "same-origin",
      redirect: "manual",
    });
  } catch {
    return { ok: false, status: 0, error: "Sin conexión con el hub." };
  }
  if (res.type === "opaqueredirect" || res.ok) {
    let data = null;
    if ((res.headers.get("Content-Type") || "").includes("application/json")) data = await res.json().catch(() => null);
    return { ok: true, status: res.status, data };
  }
  const type = res.headers.get("Content-Type") || "";
  if (type.includes("application/json")) {
    const data = await res.json().catch(() => null);
    return { ok: false, status: res.status, data, error: data?.error || "No se pudo guardar." };
  }
  return { ok: false, status: res.status, error: (await res.text()).trim() || "No se pudo guardar." };
}
