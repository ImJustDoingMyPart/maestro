<!--
  Descubiertos: unidades y timers de systemd, LXC y VMs de Proxmox que
  encontraron los agents, por host, con lo que falla primero. Mostrar los
  pasa a la portada; Volver a la bandeja los saca.
-->
<script>
  import { getContext } from "svelte";
  import { post } from "../lib/api.js";
  import SvcIcon from "../lib/SvcIcon.svelte";
  import StatePill from "../lib/StatePill.svelte";
  import { stateLabel } from "../lib/states.js";

  let { data, demo } = $props();
  const app = getContext("app");

  let query = $state("");
  let failingOnly = $state(false);
  let busy = $state("");
  let error = $state("");

  const failing = (r) => r.State === "down" || r.State === "degraded";
  function visible(r) {
    if (failingOnly && !failing(r)) return false;
    const q = query.trim().toLowerCase();
    return !q || (r.Label + " " + r.Name + " " + r.Sub).toLowerCase().includes(q);
  }
  const hosts = $derived(
    (data?.Found ?? []).map((h) => ({ ...h, Rows: (h.Rows ?? []).filter(visible) })).filter((h) => h.Rows.length),
  );

  async function act(url, key) {
    busy = key;
    error = await post(url, { key });
    busy = "";
    await app.reload();
  }
</script>

<header class="page-head">
  <a class="icon-btn" href="/" title="Volver" aria-label="Volver a Servicios"><i>arrow_back</i></a>
  <h1 class="page-title">Descubiertos</h1>
</header>
<p class="page-lead">Unidades y timers de systemd, LXC y VMs de Proxmox que encontraron los agents. No van a la portada hasta que se los muestra; una unidad que falla avisa igual.</p>

{#if error}<article class="banner error"><i>error</i><div class="banner-text"><div class="banner-title">{error}</div></div></article>{/if}

{#if data?.Found?.length}
  <div class="found-filter">
    <div class="field small prefix border round max">
      <i>search</i>
      <input type="search" placeholder="Filtrar" aria-label="Filtrar descubiertos" bind:value={query} />
    </div>
    <label class="checkbox">
      <input type="checkbox" bind:checked={failingOnly} />
      <span>Solo lo que falla</span>
    </label>
  </div>
  {#each hosts as h (h.Host)}
    <section class="found-host">
      <h2 class="section-title">{h.Host}{#if h.Failing}{" "}<StatePill state="down" label={h.Failing + " con fallas"} />{/if}</h2>
      <ul class="rows">
        {#each h.Rows as r (r.Key)}
          <li class="row-item" data-state={r.State}>
            <SvcIcon logo={r.Logo} icon={r.Icon} />
            <a class="row-main" href={r.Href}>
              <span class="row-title"><span class="dot {r.State}" title={stateLabel(r.State)}></span><span>{r.Label}</span></span>
              <span class="row-sub" title={r.Sub}>{r.Sub}</span>
            </a>
            <div class="row-actions">
              <button type="button" class="icon-btn" data-icon-pick data-key={r.Key} data-icon={r.IconSet} data-name={r.Label} title="Cambiar ícono" aria-label={"Cambiar el ícono de " + r.Label} disabled={demo}><i>imagesmode</i></button>
              {#if r.InFile}
                <button class="btn outlined" title="Está declarado en maestro.yaml: se cambia ahí" disabled><i>description</i><span class="m l">En el archivo</span></button>
              {:else if r.Shown}
                <button class="btn outlined" title="Sacarlo de la portada" disabled={demo || busy === r.Key} onclick={() => act("/discovered/unshow", r.Key)}><i>inbox</i><span class="m l">Volver a la bandeja</span></button>
              {:else}
                <button class="btn tonal" title="Pasarlo a la portada" disabled={demo || busy === r.Key} onclick={() => act("/discovered/show", r.Key)}><i>visibility</i><span class="m l">Mostrar</span></button>
              {/if}
            </div>
          </li>
        {/each}
      </ul>
    </section>
  {/each}
  {#if !hosts.length}<p class="found-empty empty-state">Nada coincide con el filtro.</p>{/if}
{:else if data}
  <article class="empty-state">
    <i>travel_explore</i>
    <p>Todavía no hay nada descubierto. Las unidades de systemd y las LXC y VMs de Proxmox llegan con los agents de esos hosts.</p>
  </article>
{/if}
