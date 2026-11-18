<!--
  Un gráfico de series de tiempo: lo dibuja web/src/charts.js con uPlot,
  que se carga aparte la primera vez que hace falta. spec es un chartSpec
  de internal/hub/hardware.go (Title, Unit, Stack, Series en JSON, ID,
  Hidden). Las series nuevas (otra Series) se vuelven a pedir; un refresco
  con las mismas no toca el gráfico.
-->
<script>
  import Eye from "./Eye.svelte";

  let { spec, ontoggle = null } = $props();

  function chart(node, series) {
    let inst = null;
    let dead = false;
    import("../../charts.js").then((m) => {
      if (!dead) inst = m.mountChart(node);
    });
    return {
      update() {
        inst?.update();
      },
      destroy() {
        dead = true;
        inst?.destroy();
      },
    };
  }
</script>

<article class="chart" class:is-hidden={spec.Hidden} data-chart data-unit={spec.Unit} data-stack={spec.Stack ? "" : null} data-series={spec.Series} data-hw-item={spec.ID || null} use:chart={spec.Series}>
  <div class="chart-head">
    <h3 class="chart-title">{spec.Title}</h3>
    <span class="chart-now" data-chart-now></span>
    {#if spec.ID && ontoggle}<Eye hidden={spec.Hidden} onclick={ontoggle} />{/if}
  </div>
  <div class="chart-plot"></div>
  <div class="chart-legend" data-chart-legend></div>
  <div class="chart-msg">Cargando…</div>
</article>
