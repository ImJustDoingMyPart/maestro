<!--
  Menú de los tres puntos. Lo abierto lo guarda la portada (uno a la vez,
  y sigue abierto aunque llegue un refresco). La lista crece desde su
  esquina: opacity y scale.
-->
<script>
  import { scale } from "svelte/transition";
  import { dur, ease } from "./motion.js";

  let { open, ontoggle, label, small = false, children } = $props();
</script>

<div class="card-menu" class:open>
  <button
    type="button"
    class="icon-btn"
    class:small
    title="Opciones"
    aria-label={"Opciones de " + label}
    aria-expanded={open}
    onclick={(e) => {
      e.stopPropagation();
      ontoggle();
    }}><i>more_vert</i></button
  >
  {#if open}
    <div
      class="card-menu-list"
      in:scale={{ start: 0.92, opacity: 0, duration: dur("--dur-m"), easing: ease }}
      out:scale={{ start: 0.96, opacity: 0, duration: dur("--dur-s"), easing: ease }}
    >
      {@render children()}
    </div>
  {/if}
</div>
