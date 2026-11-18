<!-- Selector de rango de los gráficos: vale para todos los de la página. -->
<script>
  import { onMount } from "svelte";

  const ranges = [
    ["1h", "1 h"],
    ["6h", "6 h"],
    ["24h", "24 h"],
    ["7d", "7 d"],
    ["30d", "30 d"],
    ["1y", "1 a", "1 año"],
  ];
  let current = $state("");
  let charts = null;

  onMount(() => {
    let off = () => {};
    let dead = false;
    import("../../charts.js").then((m) => {
      if (dead) return;
      charts = m;
      current = m.getRange();
      off = m.onRange((r) => (current = r));
    });
    return () => {
      dead = true;
      off();
    };
  });
</script>

<nav class="segmented chart-ranges" role="group" aria-label="Rango de los gráficos">
  {#each ranges as [r, label, title]}
    <button type="button" class:active={current === r} aria-pressed={current === r} {title} onclick={() => charts?.setRange(r)}>{label}</button>
  {/each}
</nav>
