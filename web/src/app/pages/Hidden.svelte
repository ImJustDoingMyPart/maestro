<!--
  Ocultos: las tarjetas que no se muestran en la portada (se siguen
  vigilando). Mostrar las devuelve; lo que oculta maestro.yaml no se
  cambia desde acá.
-->
<script>
  import { getContext } from "svelte";
  import { post } from "../lib/api.js";
  import { dateTime } from "../lib/format.js";

  let { data } = $props();
  const app = getContext("app");
  let error = $state("");

  async function show(key) {
    error = await post("/hidden/show", { key });
    await app.reload();
  }
</script>

<header class="page-head">
  <a class="icon-btn" href="/" title="Volver" aria-label="Volver a Servicios"><i>arrow_back</i></a>
  <h1 class="page-title">Ocultos</h1>
</header>
<p class="page-lead">No se muestran en la portada, pero se siguen vigilando y su página sigue existiendo.</p>

{#if error}<article class="banner error"><i>error</i><div class="banner-text"><div class="banner-title">{error}</div></div></article>{/if}

{#if data?.Hidden?.length}
  <ul class="rows">
    {#each data.Hidden as r (r.Key)}
      <li class="row-item">
        <span class="svc-icon"><i>visibility_off</i></span>
        <div class="row-main">
          <span class="row-title">{r.Name}</span>
          <span class="row-sub">{r.Detail} · {r.Locked ? "lo oculta maestro.yaml" : "oculto el " + dateTime(r.At)}</span>
        </div>
        <button class="btn tonal" disabled={r.Locked} title={r.Locked ? "Se muestra sacando hidden: true de maestro.yaml" : null} onclick={() => show(r.Key)}><i>visibility</i><span>Mostrar</span></button>
      </li>
    {/each}
  </ul>
{:else if data}
  <article class="empty-state">
    <i>visibility</i>
    <p>No hay nada oculto.</p>
  </article>
{/if}
