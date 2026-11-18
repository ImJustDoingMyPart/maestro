<!--
  Muestrario: cada pieza de la interfaz, por familia, con datos inventados
  (internal/hub/muestrario.go) y los componentes reales de las páginas. Lo
  que se vive como el mismo tipo de cosa tiene que verse igual; las
  medidas salen de la escala de maestro.css. El tema se elige acá sin
  cambiar el del sistema (ui() de Beer CSS).
-->
<script>
  import Card from "./Card.svelte";
  import HwCard from "../lib/HwCard.svelte";
  import StatePill from "../lib/StatePill.svelte";
  import Ranges from "../lib/Ranges.svelte";
  import { stateLabel } from "../lib/states.js";
  import { time } from "../lib/format.js";

  let { data } = $props();
  const m = $derived(data?.Muestra);

  let mode = $state("auto");
  function setMode(v) {
    mode = v;
    if (typeof ui === "function") ui("mode", v);
  }

  const none = () => ({ duration: 0 });
  let open = $state([]);
  const toggle = (k) => (open = open.includes(k) ? open.filter((x) => x !== k) : [...open, k]);

  const issues = {
    down: [[4, "«image» está repetida (también en la línea 3).", 'compose.yml: yaml: construct errors: line 1: line 4: mapping key "image" already defined at line 3']],
    degraded: [[7, "«def» no está en el .env ni en el entorno, así que queda vacía. Si el $ es parte del valor (una contraseña, por ejemplo), escribilo $$.", 'The "def" variable is not set. Defaulting to a blank string.']],
  };
</script>

<header class="page-head">
  <h1 class="page-title">Muestrario</h1>
  <div class="page-actions">
    <nav class="segmented mu-modes" role="group" aria-label="Tema">
      {#each [["auto", "Sistema"], ["light", "Claro"], ["dark", "Oscuro"]] as [v, label]}
        <button type="button" class:active={mode === v} onclick={() => setMode(v)}>{label}</button>
      {/each}
    </nav>
  </div>
</header>
<p class="page-lead">Cada pieza de la interfaz, por familia, con los mismos componentes que las páginas. Lo que se vive como el mismo tipo de cosa tiene que verse igual; las medidas salen de la escala de <code>maestro.css</code>.</p>

{#if m}
  <h2 class="section-title">Escala</h2>
  <div class="mu-grid">
    <section class="mu-box">
      <h3 class="mu-title">Espacio</h3>
      {#each ["hair", "1", "2", "3", "4", "6", "8", "10", "16"] as t}
        <div class="mu-scale"><code>--sp-{t}</code><span class="mu-bar" data-sp={t}></span></div>
      {/each}
    </section>
    <section class="mu-box">
      <h3 class="mu-title">Radios</h3>
      <div class="mu-radii">
        {#each ["xs", "s", "m", "l", "full", "icon"] as t}
          <div class="mu-radius"><span data-r={t}></span><code>--r-{t}</code></div>
        {/each}
      </div>
    </section>
    <section class="mu-box">
      <h3 class="mu-title">Tipos</h3>
      {#each ["headline", "title-l", "title-m", "label-l", "label-m", "label-s", "body-m", "body-s", "caption", "badge", "mono"] as t}
        <div class="mu-type"><code>--type-{t}</code><span data-type={t}>Jellyfin 14,2 GB</span></div>
      {/each}
    </section>
    <section class="mu-box">
      <h3 class="mu-title">Íconos</h3>
      <div class="mu-icons">
        {#each ["xs", "s", "m", "l", "xl", "xxl"] as t}
          <div class="mu-icon"><i data-icon={t}>dns</i><code>--icon-{t}</code></div>
        {/each}
      </div>
    </section>
  </div>

  <h2 class="section-title">Botones</h2>
  <div class="mu-row">
    <button type="button" class="btn filled"><i>save</i><span>Guardar</span></button>
    <button type="button" class="btn tonal"><i>visibility</i><span>Mostrar</span></button>
    <button type="button" class="btn outlined"><i>inbox</i><span>Volver a la bandeja</span></button>
    <button type="button" class="btn text">Automático</button>
    <button type="button" class="btn danger"><i>delete</i><span>Borrar</span></button>
    <button type="button" class="btn tonal" disabled><i>block</i><span>Deshabilitado</span></button>
  </div>
  <div class="mu-row">
    <button type="button" class="icon-btn" title="Normal"><i>more_vert</i></button>
    <button type="button" class="icon-btn tonal" title="Tonal"><i>open_in_new</i></button>
    <span class="icon-btn off" title="Lugar sin acción"><i>monitoring</i></span>
    <button type="button" class="icon-btn" disabled title="Deshabilitado"><i>description</i></button>
    <button type="button" class="icon-btn small" title="Chico"><i>visibility</i></button>
    <Ranges />
  </div>

  <h2 class="section-title">Encabezado de la portada</h2>
  <div class="mu-row">
    <div class="page-actions">
      <span class="updated" title="Se actualiza cada 15 segundos">Actualizado {time(data.Updated)}</span>
      <button type="button" class="btn outlined"><i>edit</i><span>Editar</span></button>
    </div>
  </div>

  <h2 class="section-title">Insignias y chips</h2>
  <div class="mu-row">
    <button type="button" class="btn outlined"><i>travel_explore</i><span>Descubiertos</span><span class="btn-badge">12</span></button>
    <button type="button" class="icon-btn" title="En la esquina de un botón"><i>stacks</i><span class="count">5</span></button>
    <span class="group-count">4</span>
    <span class="hw-count">3</span>
  </div>
  <div class="mu-row">
    <span class="stat up"><span class="dot up"></span>14 arriba</span>
    <span class="stat degraded"><span class="dot degraded"></span>1 degradado</span>
    <span class="stat down"><span class="dot down"></span>2 caídos</span>
    <span class="stat down zero"><span class="dot down"></span>0 caídos</span>
    <a class="stat link" href="/muestrario"><i>visibility_off</i>3 ocultos</a>
    <a class="host-tab active" href="/muestrario"><span class="dot up"></span>vm</a>
    <a class="host-tab" href="/muestrario"><span class="dot up"></span>adguard</a>
    <a class="host-tab" href="/muestrario"><span class="dot down"></span>pve</a>
  </div>

  <h2 class="section-title">Estado</h2>
  <div class="mu-row">
    {#each m.States as s}<StatePill state={s} label={stateLabel(s)} />{/each}
  </div>
  <div class="mu-row mu-meters">
    <div class="meter"><span class="mu-pct-30"></span></div>
    <div class="warn"><div class="meter"><span class="mu-pct-90"></span></div></div>
    <div class="check-bars">
      {#each ["up", "up", "partial", "up", "down", "none", "up", "up"] as b}<span class="bar {b}"></span>{/each}
    </div>
  </div>

  <h2 class="section-title">Avisos</h2>
  <article class="banner error">
    <i>cloud_off</i>
    <div class="banner-text">
      <div class="banner-title">El agent de pve no responde</div>
      <div class="banner-sub">Último dato: hace 3 minutos. Sus servicios figuran sin datos.</div>
    </div>
  </article>
  {#each [["down", "error"], ["degraded", "warning"]] as [state, icon]}
    <article class="small-round no-elevate state-reasons issue-box {state}">
      <nav>
        <i class="issues-icon">{icon}</i>
        <div class="max issues-list">
          {#each issues[state] as [line, msg, raw]}
            <div class="issue"><a class="issue-line" href="/muestrario">Línea {line}</a> {msg}<div class="issue-raw">{raw}</div></div>
          {/each}
        </div>
      </nav>
    </article>
  {/each}
  <p class="edit-hint mu-show"><i>info</i><span>Arrastrá las tarjetas para ordenarlas y usá el ojo para ocultarlas.</span></p>

  <h2 class="section-title">Tarjetas de la portada</h2>
  <div class="cards">
    {#each m.Cards as card, i (i)}
      <div class="card-slot">
        <Card {card} live={false} open={open.includes(card.name)} ontogglestack={() => toggle(card.name)} menu="" setmenu={() => {}} act={() => {}} send={none} receive={none} instant={() => true} />
      </div>
    {/each}
  </div>

  <h2 class="section-title">Tarjetas de Hardware</h2>
  <div class="hw-tiles balanced">
    {#each m.Tiles as c (c.Title)}<HwCard {c} />{/each}
  </div>

  <h2 class="section-title">Filas</h2>
  <ul class="rows">
    {#each m.Rows as r (r.Name)}
      <li class="row-item">
        <span class="svc-icon"><i>visibility_off</i></span>
        <div class="row-main">
          <span class="row-title">{r.Name}</span>
          <span class="row-sub">{r.Detail}</span>
        </div>
        <button type="button" class="btn tonal" disabled={r.Locked}><i>visibility</i><span>Mostrar</span></button>
      </li>
    {/each}
  </ul>

  <h2 class="section-title">Vacío</h2>
  <article class="empty-state">
    <i>visibility</i>
    <p>No hay nada oculto.</p>
  </article>
{/if}
