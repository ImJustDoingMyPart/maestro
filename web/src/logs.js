// Entrada de la pestaña Actividad: un visor por cada .log-viewer de la
// página, conectado a su data-stream.

import { Viewer } from "./viewer.js";

for (const root of document.querySelectorAll(".log-viewer[data-stream]")) new Viewer(root);
