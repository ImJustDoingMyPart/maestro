// Portada hecha con Svelte: se monta en #portada (web/templates/portada.html).
import { mount } from "svelte";
import Portada from "./portada/Portada.svelte";

const target = document.getElementById("portada");
if (target) {
  mount(Portada, {
    target,
    props: { live: "live" in target.dataset, demo: "demo" in target.dataset },
  });
}
