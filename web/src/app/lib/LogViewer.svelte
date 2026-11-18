<!--
  Visor de logs (web/src/viewer.js): el de la pestaña Actividad. Se conecta
  a stream (Server-Sent Events) al montarse y cierra la conexión al irse.
  replicas son los nombres de los contenedores, si hay más de uno.
-->
<script>
  import { onMount } from "svelte";
  import { Viewer } from "../../viewer.js";

  let { stream, replicas = [] } = $props();
  let root;

  onMount(() => {
    const v = new Viewer(root);
    return () => v.destroy();
  });
</script>

<section class="log-viewer" bind:this={root} data-stream={stream} data-replicas={replicas.length > 1 ? replicas.join(" ") : null}>
  <nav class="log-toolbar wrap small-space">
    <div class="field small prefix round border no-margin log-search">
      <i>search</i>
      <input type="search" placeholder="Buscar" aria-label="Buscar en los logs" autocomplete="off" />
    </div>
    <!-- svelte-ignore a11y_no_noninteractive_element_to_interactive_role -->
    <nav class="group connected log-levels" role="radiogroup" aria-label="Nivel">
      <button class="left-round small active" data-level="all">Todo</button>
      <button class="no-round small" data-level="warn">Avisos</button>
      <button class="right-round small" data-level="error">Errores</button>
    </nav>
    <span class="chip no-border surface-container log-status" role="status"><span class="dot unknown"></span><span>Conectando…</span></span>
  </nav>
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div class="log-scroll" tabindex="0" aria-label="Logs">
    <div class="log-spacer"></div>
    <div class="log-window"></div>
  </div>
  <button class="log-bottom extend circle small-elevate" hidden aria-label="Ir al final">
    <i>arrow_downward</i><span>Ir al final</span>
  </button>
  <dialog class="bottom log-detail">
    <nav>
      <h6 class="max small">Línea</h6>
      <button class="circle transparent" data-copy title="Copiar" aria-label="Copiar"><i>content_copy</i></button>
      <button class="circle transparent" data-close title="Cerrar" aria-label="Cerrar"><i>close</i></button>
    </nav>
    <div class="small-text secondary-text log-detail-meta"></div>
    <pre class="log-detail-text"></pre>
  </dialog>
</section>
