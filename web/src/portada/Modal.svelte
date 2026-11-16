<!--
  Diálogo modal: un <dialog> nativo (foco atrapado, Escape, fondo oscuro)
  que entra y sale con la Web Animations API, solo opacity y transform. La
  transición propia de Beer queda apagada (.modal en maestro.css). Se
  monta abierto; onclose lo pide el padre, que lo desmonta cuando termina
  la salida (closed).
-->
<script>
  import { onMount } from "svelte";
  import { dur, ease } from "./motion.js";

  let { title, onclose, children, class: cls = "" } = $props();
  let el;
  let leaving = false;

  // Las curvas de la WAAPI son texto: se usa el mismo token.
  const curve = () => getComputedStyle(document.documentElement).getPropertyValue("--ease").trim() || "ease";
  const shift = () => getComputedStyle(document.documentElement).getPropertyValue("--sp-4").trim() || "1rem";

  onMount(() => {
    el.showModal();
    el.animate(
      [
        { opacity: 0, transform: `translate(-50%, ${shift()})` },
        { opacity: 1, transform: "translate(-50%, 0)" },
      ],
      { duration: dur("--dur-m"), easing: curve() },
    );
    el.querySelector("[data-focus]")?.focus();
  });

  // close anima la salida y después cierra de verdad.
  export function close() {
    if (leaving) return;
    leaving = true;
    const a = el.animate(
      [
        { opacity: 1, transform: "translate(-50%, 0)" },
        { opacity: 0, transform: `translate(-50%, ${shift()})` },
      ],
      { duration: dur("--dur-s"), easing: curve(), fill: "forwards" },
    );
    a.onfinish = () => {
      el.close();
      onclose();
    };
  }

  function cancel(e) {
    e.preventDefault(); // Escape: se cierra con la animación
    close();
  }
  function backdrop(e) {
    if (e.target === el) close(); // tocar el fondo
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<dialog bind:this={el} class="small-round form-dialog modal {cls}" oncancel={cancel} onclick={backdrop}>
  <h6>{title}</h6>
  {@render children(close)}
</dialog>
