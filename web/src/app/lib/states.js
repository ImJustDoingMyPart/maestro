// Las palabras de cada estado (State.Label en internal/hub/pages.go).
const labels = { up: "Arriba", degraded: "Degradado", down: "Caído", stopped: "Detenido" };

export const stateLabel = (s) => labels[s] ?? "Sin datos";
