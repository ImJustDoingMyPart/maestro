// Entrada de la aplicación: se monta en #app (web/templates/app.html).
// Las páginas pesadas (el editor de Compose con CodeMirror, los gráficos
// con uPlot) son chunks aparte que se cargan al entrar a ellas.
import { mount } from "svelte";
import App from "./App.svelte";
import "./lib/logos.js";
// El CSS de uPlot va con la entrada: un chunk que se carga aparte no trae
// su CSS solo.
import "uplot/dist/uPlot.min.css";

const target = document.getElementById("app");
if (target) mount(App, { target, props: { demo: "demo" in target.dataset } });
