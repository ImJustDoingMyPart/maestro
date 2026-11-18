<!--
  Página de un servicio: cabecera con el estado, contenedores y motivos,
  el panel de checks, y las pestañas Actividad (el visor de logs, o los
  cambios de estado si no tiene logs), Métricas y Compose. Se refresca
  cada 15 segundos (como la portada); el visor sigue por su cuenta.
-->
<script>
  import { getContext, onMount } from "svelte";
  import SvcIcon from "../lib/SvcIcon.svelte";
  import LogViewer from "../lib/LogViewer.svelte";
  import ReportButton from "../lib/ReportButton.svelte";
  import Chart from "../lib/Chart.svelte";
  import Ranges from "../lib/Ranges.svelte";
  import { stateLabel } from "../lib/states.js";
  import { dateTime, time } from "../lib/format.js";

  let { data } = $props();
  const app = getContext("app");

  const svc = $derived(data?.Svc);
  const metrics = $derived(data?.Tab === "metrics");

  onMount(() => {
    const t = setInterval(() => {
      if (!document.hidden && !document.querySelector("dialog[open]")) app.reload();
    }, 15000);
    return () => clearInterval(t);
  });

  const chipDot = (c) => (c.State === "running" ? (c.Health === "unhealthy" ? "down" : "up") : "down");
  const reasonIcon = (s) => (s === "down" ? "error" : s === "degraded" ? "warning" : "help");
  const barTitle = (b) => dateTime(b.From) + " a " + time(b.To).slice(0, 5) + ": " + b.Summary;
</script>

{#if svc}
  <header class="transparent service-header">
    <nav>
      <a class="button circle transparent" href="/" title="Volver" aria-label="Volver a Servicios"><i>arrow_back</i></a>
      <span class="m l"><SvcIcon logo={svc.Logo} icon={svc.Icon} /></span>
      <div class="max">
        <h5 class="small">{svc.Name}</h5>
        <div class="small-text secondary-text">{svc.Description}</div>
      </div>
      <span class="status {svc.State}" title={stateLabel(svc.State)}>
        <span class="dot {svc.State}"></span>
        <span class="small-text">{stateLabel(svc.State)}</span>
      </span>
      <ReportButton url={svc.ReportURL} />
      {#if svc.URL}
        <a class="btn tonal m l" href={svc.URL} target="_blank" rel="noopener"><i>open_in_new</i><span>Abrir</span></a>
      {/if}
    </nav>
  </header>

  <nav class="wrap small-space service-facts">
    <span class="chip no-border surface-container"><i>category</i><span>{svc.Source}</span></span>
    {#if svc.Host}<span class="chip no-border surface-container"><i>dns</i><span>{svc.Host}</span></span>{/if}
    {#each svc.Containers ?? [] as c (c.Name)}
      <span class="chip no-border surface-container" title={c.Image}>
        <span class="dot {chipDot(c)}"></span>
        <span>{c.Name} · {c.State}{c.Health ? " (" + c.Health + ")" : ""}{c.Restarts ? " · " + c.Restarts + " reinicios" : ""}</span>
      </span>
    {/each}
    {#if svc.URL}<a class="chip no-border surface-container s" href={svc.URL} target="_blank" rel="noopener"><i>open_in_new</i><span>Abrir</span></a>{/if}
  </nav>

  {#if svc.Reasons?.length && svc.State !== "up"}
    <article class="small-round no-elevate state-reasons {svc.State}">
      <nav>
        <i>{reasonIcon(svc.State)}</i>
        <div class="max">{#each svc.Reasons as r}<div>{r}</div>{/each}</div>
      </nav>
    </article>
  {/if}

  {#if svc.Checks?.length}
    <section class="checks">
      {#each svc.Checks as ch, i (ch.Kind + i)}
        <article class="small-round no-elevate surface-container-low check">
          <nav class="wrap small-space">
            <span class="dot {ch.State}" title={stateLabel(ch.State)}></span>
            <span class="bold">{ch.Kind}</span>
            {#if ch.Target}<span class="small-text secondary-text check-target">{ch.Target}</span>{/if}
            <div class="max"></div>
            {#if ch.Uptime}<span class="small-text" title="Disponibilidad en las últimas 24 horas">{ch.Uptime}</span>{/if}
          </nav>
          <div class="check-bars" role="img" aria-label="Historial de las últimas 24 horas">
            {#each ch.Bars ?? [] as b}<span class="bar {b.Class}" title={barTitle(b)}></span>{/each}
          </div>
          <div class="small-text secondary-text">
            {#if !time(ch.LastAt)}Todavía no corrió.{:else}{ch.Last}{ch.Latency ? " · " + ch.Latency : ""} · {time(ch.LastAt)}{/if}
            {#if ch.Cert}<span class:cert-warn={ch.CertWarn}>· {ch.Cert}</span>{/if}
          </div>
        </article>
      {/each}
    </section>
  {/if}

  <div class="tabs left-align">
    <!-- svelte-ignore a11y_missing_attribute -->
    {#if metrics}<a href={svc.PageURL}><i>history</i><span>Actividad</span></a>{:else}<a class="active" aria-current="page"><i>history</i><span>Actividad</span></a>{/if}
    {#if svc.MetricsURL}
      <!-- svelte-ignore a11y_missing_attribute -->
      {#if metrics}<a class="active" aria-current="page"><i>monitoring</i><span>Métricas</span></a>{:else}<a href={svc.MetricsURL}><i>monitoring</i><span>Métricas</span></a>{/if}
    {/if}
    {#if svc.StackURL}<a href={svc.StackURL}><i>stacks</i><span>Compose</span></a>{/if}
  </div>

  {#if metrics}
    <Ranges />
    <div class="hw-charts">
      {#each data.Charts ?? [] as c (c.Title)}<Chart spec={c} />{/each}
    </div>
    <p class="small-text secondary-text">Métricas de cada contenedor según Docker: la CPU en porcentaje de un núcleo y la memoria sin la caché de archivos, como docker stats.</p>
  {:else if svc.LogsURL}
    {#key svc.LogsURL}
      <LogViewer stream={svc.LogsURL} replicas={(svc.Containers ?? []).map((c) => c.Name)} />
    {/key}
  {:else}
    <article class="small-round no-elevate surface-container-low center-align"><p>{svc.NoLogs}</p></article>
    {#if svc.Events?.length}
      <ul class="list border state-events">
        {#each svc.Events as e}
          <li>
            <span class="dot {e.State}"></span>
            <div class="max"><div>{stateLabel(e.State)}{e.Reason ? " · " + e.Reason : ""}</div></div>
            <span class="small-text secondary-text">{dateTime(e.At)}</span>
          </li>
        {/each}
      </ul>
    {:else if svc.Checks?.length}
      <p class="small-text secondary-text center-align">Sin cambios de estado en las últimas 24 horas.</p>
    {/if}
  {/if}
{:else if data}
  <article class="small-round no-elevate surface-container-low center-align">
    <h6>Ese servicio no existe</h6>
    <p>Puede que se haya dado de baja o que su agent todavía no haya respondido.</p>
    <a class="button" href="/">Volver a Servicios</a>
  </article>
{/if}
