// Pedidos al hub. Las acciones usan los mismos formularios que la portada
// de antes (POST con campos de formulario): sin HX-Request, el hub responde
// con una redirección a "/", que acá no se sigue; un error trae el texto
// en el cuerpo (400, 409).

export async function getPortada() {
  const res = await fetch("/api/portada", { credentials: "same-origin" });
  if (res.redirected && new URL(res.url).pathname === "/login") {
    location.href = "/login";
    throw new Error("sesión vencida");
  }
  if (!res.ok) throw new Error("No se pudo leer la portada (" + res.status + ").");
  return res.json();
}

// post manda un formulario. Devuelve "" si salió bien o el texto del error.
export async function post(url, fields) {
  const body = new URLSearchParams();
  for (const [k, v] of Object.entries(fields)) {
    if (Array.isArray(v)) v.forEach((x) => body.append(k, x));
    else if (v !== undefined && v !== null) body.append(k, v);
  }
  try {
    const res = await fetch(url, { method: "POST", body, credentials: "same-origin", redirect: "manual" });
    if (res.type === "opaqueredirect" || res.ok) return "";
    return (await res.text()).trim() || "No se pudo guardar.";
  } catch {
    return "Sin conexión con el hub.";
  }
}
