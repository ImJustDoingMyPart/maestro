<!--
  Tarjeta de la portada. Lo que se mueve:
    - los servicios de un stack se despliegan con el alto (fold, en
      motion.js);
    - el punto de estado, cuando cambia, aparece con el color nuevo;
    - un servicio que se saca del stack viaja a su tarjeta (send/receive,
      el crossfade de la portada).
-->
<script>
  import { scale } from "svelte/transition";
  import { flip } from "svelte/animate";
  import Menu from "../lib/Menu.svelte";
  import { dur, ease, fold } from "../lib/motion.js";

  let { card, live, open, ontogglestack, menu, setmenu, act, send, receive, instant } = $props();

  const members = $derived(card.members ?? []);
  const title = $derived(card.reason ? card.stateLabel + ": " + card.reason : card.stateLabel);

  // Una tarjeta de stack se despliega tocándola en cualquier lugar que no
  // sea un link, un botón o el menú.
  function onclick(e) {
    if (!members.length || document.body.classList.contains("editing")) return;
    if (e.target.closest("a, button, .card-menu, .stack-members")) return;
    ontogglestack();
  }
  const flipDur = () => (instant() ? 0 : dur("--dur-m"));

  // Levantar un stack bajado: el botón se apaga mientras el hub arranca
  // la acción (docker compose up -d sigue en el agent).
  let upping = $state(false);
  async function up() {
    upping = true;
    try {
      await act("up", { href: card.upHref, name: card.name });
    } finally {
      upping = false;
    }
  }
</script>

<!-- Prender o apagar los avisos a Telegram (en un stack, todos sus
     servicios; x.alert es si alguno avisa). -->
{#snippet alertItem(x)}
  <button type="button" disabled={x.alertLocked} title={x.alertLocked ? "Viene de maestro.yaml" : null} onclick={() => act("alert", { key: x.key, alert: x.alert ? "0" : "1" })}>
    {#if x.alert}<i>notifications_off</i><span>Dejar de avisar</span>{:else}<i>notifications</i><span>Avisar por Telegram</span>{/if}
  </button>
{/snippet}

{#snippet dot(state, label)}
  {#key state}
    <span class="dot {state}" title={label} in:scale={{ start: 0.3, duration: dur("--dur-l"), easing: ease }}></span>
  {/key}
{/snippet}

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<article class="svc-card" class:stack={members.length} class:open data-state={card.state} {onclick}>
  <div class="svc-head">
    <span class="svc-icon" aria-hidden="true">
      {#if card.logo}
        {#if card.logo.srcDark}
          <picture><source media="(prefers-color-scheme: dark)" srcset={card.logo.srcDark} /><img src={card.logo.src} alt="" loading="lazy" decoding="async" /></picture>
        {:else}
          <img src={card.logo.src} alt="" loading="lazy" decoding="async" class:mono={card.logo.mono} />
        {/if}
      {:else}
        <i>{card.icon}</i>
      {/if}
    </span>
    <div class="svc-text">
      <div class="svc-name" title={card.name}>
        {#if card.href}<a class="stretched" href={card.href}>{card.name}</a>{:else}{card.name}{/if}
      </div>
      {#if card.reason && card.state !== "up"}
        <div class="svc-sub reason {card.state}" title={card.reason}>{card.reason}</div>
      {:else}
        <div class="svc-sub" title={card.description}>{card.description || " "}</div>
      {/if}
    </div>
    {#if card.key && live}
      <Menu open={menu === card.key} ontoggle={() => setmenu(card.key)} label={card.name}>
        <button type="button" disabled={card.nameLocked} title={card.nameLocked ? "El nombre viene de maestro.yaml" : null} onclick={() => act("rename", { key: card.key, name: card.name })}>
          <i>edit</i><span>Cambiar nombre</span>
        </button>
        <button type="button" data-icon-pick data-key={card.key} data-icon={card.iconSet} data-name={card.name} disabled={card.iconLocked} title={card.iconLocked ? "El ícono viene de maestro.yaml" : null} onclick={() => setmenu("")}>
          <i>imagesmode</i><span>Cambiar ícono</span>
        </button>
        <button type="button" disabled={card.groupLocked} title={card.groupLocked ? "El grupo viene de maestro.yaml" : null} onclick={() => act("move", { key: card.key, group: card.group })}>
          <i>drive_file_move</i><span>Mover a grupo</span>
        </button>
        {#if card.editHref}<a href={card.editHref}><i>tune</i><span>Editar</span></a>{/if}
        {#if card.separated}
          <button type="button" disabled={card.separateLocked} title={card.separateLocked ? "Viene de maestro.yaml" : null} onclick={() => act("separate", { key: card.key, separate: "0" })}>
            <i>call_merge</i><span>Volver al stack</span>
          </button>
        {/if}
        {@render alertItem(card)}
        {#if card.found}
          <button type="button" onclick={() => act("unshow", { key: card.key })}><i>inbox</i><span>Volver a la bandeja</span></button>
        {:else}
          <button type="button" disabled={card.hiddenLocked} title={card.hiddenLocked ? "maestro.yaml dice hidden: false" : null} onclick={() => act("hide", { key: card.key })}>
            <i>visibility_off</i><span>Ocultar</span>
          </button>
        {/if}
      </Menu>
    {:else}
      <span class="menu-spacer"></span>
    {/if}
  </div>
  <div class="svc-foot">
    <span class="state-pill {card.state}" {title}>{@render dot(card.state, card.stateLabel)}{card.stateLabel}</span>
    <nav class="svc-actions">
      {#if members.length}
        <button type="button" class="icon-btn above" aria-expanded={open} title={"Servicios del stack (" + members.length + ")"} aria-label={"Ver los " + members.length + " servicios del stack"} onclick={ontogglestack} data-stack-toggle>
          <i>stacks</i><span class="count">{members.length}</span>
        </button>
      {:else}
        <span class="icon-btn off" title="Un solo servicio"><i>stacks</i></span>
      {/if}
      {#if card.metricsHref}
        <a class="icon-btn above" href={card.metricsHref} title="Métricas" aria-label={"Métricas de " + card.name}><i>monitoring</i></a>
      {:else}
        <span class="icon-btn off" title="Sin métricas: no es un contenedor"><i>monitoring</i></span>
      {/if}
      {#if card.stackHref}
        <a class="icon-btn above" href={card.stackHref} title="Compose" aria-label={"Compose de " + card.name}><i>description</i></a>
      {:else}
        <span class="icon-btn off" title="No es de compose"><i>description</i></span>
      {/if}
      {#if card.upHref && live}
        <button type="button" class="icon-btn tonal above" disabled={upping} title="Levantar el stack (docker compose up -d)" aria-label={"Levantar " + card.name} onclick={up}><i>play_arrow</i></button>
      {:else if card.url}
        <a class="icon-btn tonal above" href={card.url} target="_blank" rel="noopener" title={"Abrir " + card.url} aria-label={"Abrir " + card.name}><i>open_in_new</i></a>
      {:else}
        <span class="icon-btn off" title="Sin dirección web"><i>open_in_new</i></span>
      {/if}
    </nav>
  </div>
  {#if members.length && open}
    <ul class="stack-members shown" transition:fold>
      {#each members as m (m.key || m.name)}
        <li class="member-row" animate:flip={{ duration: flipDur, easing: ease }} in:receive={{ key: m.key }} out:send={{ key: m.key }}>
          {@render dot(m.state, m.stateLabel)}
          <div class="member-text">
            <a href={m.href} title={m.name}>{m.name}</a>
            {#if m.reason && m.state !== "up"}<span class="reason {m.state}" title={m.reason}>{m.reason}</span>{/if}
          </div>
          <span class="member-state">{m.stateLabel}</span>
          {#if m.key && live}
            <Menu open={menu === m.key} ontoggle={() => setmenu(m.key)} label={m.name} small>
              <button type="button" disabled={m.nameLocked} title={m.nameLocked ? "El nombre viene de maestro.yaml" : null} onclick={() => act("rename", { key: m.key, name: m.name })}>
                <i>edit</i><span>Cambiar nombre</span>
              </button>
              <button type="button" disabled={m.separateLocked} title={m.separateLocked ? "Viene de maestro.yaml" : null} onclick={() => act("separate", { key: m.key, separate: "1" })}>
                <i>call_split</i><span>Sacar del stack</span>
              </button>
              {@render alertItem(m)}
              <button type="button" disabled={m.hiddenLocked} title={m.hiddenLocked ? "maestro.yaml dice hidden: false" : null} onclick={() => act("hide", { key: m.key })}>
                <i>visibility_off</i><span>Ocultar</span>
              </button>
            </Menu>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</article>
