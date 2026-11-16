<!--
  Portada de servicios. Los datos llegan de /api/portada cada 15 segundos
  y se reemplazan en el estado: Svelte compara por clave y solo toca lo que
  cambió, así una tarjeta sigue siendo la misma de una vuelta a la otra
  (un stack abierto o un menú abierto siguen así).

  Lo que se mueve, siempre con opacity y transform salvo el despliegue de
  un stack (el alto de una sola tarjeta):
    - una tarjeta que cambia de lugar se desliza (animate:flip);
    - una que pasa a otro grupo viaja hasta allá (crossfade); una que se
      oculta se achica y se va, y las demás ocupan su lugar;
    - banners, la barra del modo edición y los diálogos entran y salen.
-->
<script>
  import { onMount, tick } from "svelte";
  import { flip } from "svelte/animate";
  import { crossfade, fade, fly, scale } from "svelte/transition";
  import Card from "./Card.svelte";
  import Modal from "./Modal.svelte";
  import { getPortada, post } from "./api.js";
  import { dur, ease } from "./motion.js";

  let { live = false, demo = false } = $props();

  let data = $state(null);
  let loadError = $state("");
  let editing = $state(false);
  let menu = $state(""); // clave del menú abierto
  let dialog = $state(null); // { kind, ... } del diálogo abierto
  let dialogError = $state("");
  let layoutError = $state("");
  // instant apaga los deslizamientos mientras se guarda lo arrastrado:
  // SortableJS ya dejó la tarjeta en su lugar.
  let instant = false;
  let dragging = false;

  // ---------- Stacks abiertos (sobreviven a recargar la página) ----------
  const KEY = "maestro.stacks.abiertos";
  function loadOpen() {
    try {
      return JSON.parse(sessionStorage.getItem(KEY) || "[]");
    } catch {
      return [];
    }
  }
  let openStacks = $state(loadOpen());
  function toggleStack(key) {
    openStacks = openStacks.includes(key) ? openStacks.filter((k) => k !== key) : [...openStacks, key];
    try {
      sessionStorage.setItem(KEY, JSON.stringify(openStacks));
    } catch {
      /* sin almacenamiento: se cierran al recargar */
    }
  }

  // ---------- Datos ----------
  async function refresh() {
    if (dragging) return;
    try {
      data = await getPortada();
      loadError = "";
    } catch (e) {
      loadError = e.message;
    }
  }
  onMount(() => {
    refresh();
    const t = setInterval(refresh, 15000);
    const vis = () => document.visibilityState === "visible" && refresh();
    document.addEventListener("visibilitychange", vis);
    return () => {
      clearInterval(t);
      document.removeEventListener("visibilitychange", vis);
    };
  });

  const updated = $derived(data?.updated ? new Date(data.updated).toLocaleTimeString("es-AR") : "");
  const groupNames = $derived((data?.groups ?? []).filter((g) => !g.otros).map((g) => g.name));
  const plural = (n, one, many) => n + " " + (n === 1 ? one : many);

  // Una tarjeta que cambia de grupo sale de uno y entra en el otro: el
  // crossfade la lleva de un lugar al otro. Sin pareja (se ocultó, o es
  // nueva) se achica o crece en su lugar.
  const [send, receive] = crossfade({
    duration: (d) => (instant ? 0 : Math.min(dur("--dur-l"), dur("--dur-m") + d / 4)),
    easing: ease,
    fallback: (node) => scale(node, { start: 0.9, opacity: 0, duration: instant ? 0 : dur("--dur-m"), easing: ease }),
  });
  const flipDur = () => (instant ? 0 : dur("--dur-m"));

  // ---------- Menú y acciones ----------
  function setmenu(key) {
    menu = menu === key ? "" : key;
  }
  function outside(e) {
    if (!e.target.closest(".card-menu")) menu = "";
  }
  function keydown(e) {
    if (e.key === "Escape") menu = "";
  }

  async function run(url, fields) {
    const err = await post(url, fields);
    if (err) return err;
    await refresh();
    return "";
  }

  // act lo llaman las tarjetas desde su menú.
  async function act(kind, p) {
    menu = "";
    switch (kind) {
      case "rename":
        dialogError = "";
        dialog = { kind: "rename", key: p.key, name: p.name };
        break;
      case "move":
        dialogError = "";
        dialog = { kind: "move", key: p.key, group: p.group };
        break;
      case "separate":
        flash(await run("/separate", p));
        break;
      case "alert":
        flash(await run("/alert", p));
        break;
      case "unshow":
        flash(await run("/discovered/unshow", p));
        break;
      case "hide":
        flash(await run("/hidden", p));
        break;
    }
  }

  let flashTimer;
  function flash(msg) {
    if (!msg) return;
    layoutError = msg;
    clearTimeout(flashTimer);
    flashTimer = setTimeout(() => (layoutError = ""), 5000);
  }

  // submit manda el formulario de un diálogo; si sale bien lo cierra.
  async function submit(e, url, close) {
    e.preventDefault();
    const fields = Object.fromEntries(new FormData(e.currentTarget, e.submitter));
    const err = await run(url, fields);
    if (err) dialogError = err;
    else close();
  }

  // ---------- Modo edición ----------
  function setEditing(on) {
    editing = on;
    document.body.classList.toggle("editing", on);
    menu = "";
    if (!on) refresh();
  }

  // Arrastrar con SortableJS (anda con el dedo). SortableJS mueve el
  // elemento en la página; al soltar se lo devuelve a su lugar y se cambia
  // el estado, que es lo que manda: así Svelte no pierde la cuenta.
  // Se rearman después de cada refresco (puede haber grupos nuevos); gen
  // descarta un armado que quedó viejo mientras esperaba a la página.
  let sortables = [];
  let gen = 0;
  function stopSorting() {
    gen++;
    sortables.forEach((s) => s.destroy());
    sortables = [];
  }
  async function startSorting() {
    const mine = ++gen;
    await tick();
    if (mine !== gen || !window.Sortable || !editing) return;
    sortables.forEach((s) => s.destroy());
    sortables = [];
    const common = {
      animation: dur("--dur-m"),
      easing: getComputedStyle(document.documentElement).getPropertyValue("--ease").trim(),
      delay: 250,
      delayOnTouchOnly: true,
      forceFallback: true,
      fallbackTolerance: 4,
      ghostClass: "drag-ghost",
      chosenClass: "drag-chosen",
    };
    let next = null;
    document.querySelectorAll("#services .group .cards").forEach((grid) => {
      sortables.push(
        Sortable.create(grid, {
          ...common,
          group: "cards",
          draggable: "[data-key]",
          filter: ".card-menu, .svc-actions",
          preventOnFilter: false,
          onStart: (e) => {
            dragging = true;
            next = e.item.nextSibling;
          },
          onEnd: (e) => {
            e.from.insertBefore(e.item, next); // la página vuelve a como la dejó Svelte
            dragging = false;
            const from = e.from.closest(".group").dataset.group;
            const to = e.to.closest(".group").dataset.group;
            if (from === to && e.oldDraggableIndex === e.newDraggableIndex) return;
            moveCard(e.item.dataset.key, from, to, e.newDraggableIndex);
          },
        }),
      );
    });
    const list = document.querySelector("#services .groups");
    if (list) {
      let gnext = null;
      sortables.push(
        Sortable.create(list, {
          ...common,
          draggable: ".group:not(.otros)",
          handle: ".group-handle",
          onStart: (e) => {
            dragging = true;
            gnext = e.item.nextSibling;
          },
          onEnd: (e) => {
            list.insertBefore(e.item, gnext);
            dragging = false;
            if (e.oldDraggableIndex === e.newDraggableIndex) return;
            moveGroup(e.oldDraggableIndex, e.newDraggableIndex);
          },
        }),
      );
    }
  }
  $effect(() => {
    if (editing && data) startSorting();
    else stopSorting();
  });

  async function settle(fn) {
    instant = true;
    fn();
    await tick();
    requestAnimationFrame(() => (instant = false));
  }

  function moveCard(key, fromName, toName, index) {
    const from = data.groups.find((g) => g.name === fromName);
    const to = data.groups.find((g) => g.name === toName);
    const i = from.cards.findIndex((c) => c.key === key);
    if (i < 0) return;
    settle(() => {
      const [card] = from.cards.splice(i, 1);
      to.cards.splice(index, 0, card);
      to.empty = false;
    });
    const keys = to.cards.map((c) => c.key);
    post("/layout/cards", { group: to.otros ? "" : toName, key: keys }).then((err) => {
      if (err) {
        flash(err);
        refresh(); // deshace lo arrastrado
      }
    });
  }

  function moveGroup(from, to) {
    const movable = data.groups.filter((g) => !g.otros);
    const [g] = movable.splice(from, 1);
    movable.splice(to, 0, g);
    settle(() => {
      data.groups = [...movable, ...data.groups.filter((g) => g.otros)];
    });
    post("/layout/groups", { name: movable.map((g) => g.name) }).then((err) => {
      if (err) {
        flash(err);
        refresh();
      }
    });
  }

  function groupDialog(kind, name) {
    dialogError = "";
    dialog = { kind, name };
  }
</script>

<svelte:document onclick={outside} onkeydown={keydown} />

<header class="page-head">
  <h1 class="page-title">Servicios</h1>
  {#if demo}<span class="chip small tertiary-container no-border">demo</span>{/if}
  <div class="page-actions">
    {#if updated}
      {#key updated}
        <span class="updated" title="Se actualiza cada 15 segundos" in:fade={{ duration: dur("--dur-m"), easing: ease }}>Actualizado {updated}</span>
      {/key}
    {/if}
    {#if live && !demo}
      <button type="button" class="btn outlined edit-toggle" aria-pressed={editing} onclick={() => setEditing(!editing)}>
        {#if editing}<i>check</i><span>Listo</span>{:else}<i>edit</i><span>Editar</span>{/if}
      </button>
      <div class="s">
        <form method="post" action="/logout"><button class="icon-btn" title="Salir" aria-label="Salir"><i>logout</i></button></form>
      </div>
    {/if}
  </div>
</header>

{#if loadError}
  <article class="banner error" transition:fly={{ y: -8, duration: dur("--dur-m"), easing: ease }}>
    <i>cloud_off</i>
    <div class="banner-text"><div class="banner-title">{loadError}</div><div class="banner-sub">Se vuelve a intentar cada 15 segundos.</div></div>
  </article>
{/if}

{#if data}
  <div id="services">
    {#each data.hostProblems as h (h.name)}
      <article class="banner error" transition:fly={{ y: -8, duration: dur("--dur-m"), easing: ease }}>
        <i>cloud_off</i>
        <div class="banner-text">
          <div class="banner-title">El agent de {h.name} no responde</div>
          <div class="banner-sub">
            {h.lastOK ? "Último dato: " + new Date(h.lastOK).toLocaleTimeString("es-AR") : "Nunca respondió"}. Sus servicios figuran sin datos. {h.err}
          </div>
        </div>
      </article>
    {/each}
    {#if data.foundFailing}
      <a class="banner error" href="/discovered" transition:fly={{ y: -8, duration: dur("--dur-m"), easing: ease }}>
        <i>error</i>
        <div class="banner-text">
          <div class="banner-title">{data.foundFailing === 1 ? "Una unidad descubierta falla" : data.foundFailing + " unidades descubiertas fallan"}</div>
          <div class="banner-sub">No están en la portada, pero avisan igual. Están primeras en Descubiertos.</div>
        </div>
        <i>chevron_right</i>
      </a>
    {/if}

    <div class="summary">
      <div class="summary-states">
        <span class="stat up"><span class="dot up"></span>{data.summary.up} arriba</span>
        {#if data.summary.degraded}<span class="stat degraded"><span class="dot degraded"></span>{plural(data.summary.degraded, "degradado", "degradados")}</span>{/if}
        <span class="stat down" class:zero={!data.summary.down}><span class="dot down"></span>{plural(data.summary.down, "caído", "caídos")}</span>
        {#if data.summary.unknown}<span class="stat unknown"><span class="dot unknown"></span>{data.summary.unknown} sin datos</span>{/if}
        {#if data.summary.stopped}<span class="stat stopped"><span class="dot stopped"></span>{plural(data.summary.stopped, "detenido", "detenidos")}</span>{/if}
      </div>
      <div class="summary-links">
        {#if data.foundPending}
          <a class="stat link" href="/discovered" title="Unidades, timers, LXC y VMs que encontraron los agents y no están en la portada"><i>travel_explore</i>{plural(data.foundPending, "descubierto", "descubiertos")}</a>
        {/if}
        {#if data.hiddenCount}
          <a class="stat link" href="/hidden" title="Tarjetas ocultas"><i>visibility_off</i>{plural(data.hiddenCount, "oculto", "ocultos")}</a>
        {/if}
      </div>
    </div>

    {#if !data.groups.length}
      <article class="empty-state">
        <i>hourglass_empty</i>
        <p>{live ? "Todavía no hay servicios: esperando a los agents." : "No hay servicios."}</p>
      </article>
    {/if}

    <div class="groups">
      {#each data.groups as g (g.name)}
        <section class="group" class:empty={g.empty} class:otros={g.otros} data-group={g.name} animate:flip={{ duration: flipDur, easing: ease }} transition:fade={{ duration: dur("--dur-m"), easing: ease }}>
          <div class="group-head">
            {#if live && !g.otros && !g.orderLocked}<i class="edit-only group-handle" title="Arrastrar para ordenar los grupos" aria-hidden="true">drag_indicator</i>{/if}
            <h2 class="group-title">{g.name}</h2>
            {#if !g.empty}<span class="group-count">{g.cards.length}</span>{/if}
            <div class="max"></div>
            {#if live && !demo}
              <button type="button" class="icon-btn edit-only" disabled={g.otros || g.locked} title={g.otros ? "Otros es el grupo de las tarjetas sin grupo" : g.locked ? "El grupo viene de maestro.yaml" : "Cambiar nombre"} aria-label={"Cambiar nombre del grupo " + g.name} onclick={() => groupDialog("group-rename", g.name)}><i>edit</i></button>
              <button type="button" class="icon-btn edit-only" disabled={g.otros || g.locked} title={g.otros ? "Otros es el grupo de las tarjetas sin grupo" : g.locked ? "El grupo viene de maestro.yaml" : "Borrar"} aria-label={"Borrar el grupo " + g.name} onclick={() => groupDialog("group-delete", g.name)}><i>delete</i></button>
            {/if}
          </div>
          <div class="cards">
            {#each g.cards as card (card.key || card.name)}
              <div class="card-slot" data-key={card.key} animate:flip={{ duration: flipDur, easing: ease }} in:receive={{ key: card.key }} out:send={{ key: card.key }}>
                <Card
                  {card}
                  live={live && !demo}
                  open={openStacks.includes(card.key)}
                  ontogglestack={() => toggleStack(card.key)}
                  {menu}
                  {setmenu}
                  {act}
                  {send}
                  {receive}
                  instant={() => instant}
                />
              </div>
            {/each}
            {#if g.empty}<p class="empty-hint">Grupo vacío: arrastrá tarjetas acá.</p>{/if}
          </div>
        </section>
      {/each}
    </div>

    {#if editing}
      <nav class="edit-bar" transition:fly={{ y: 16, duration: dur("--dur-m"), easing: ease }}>
        <button type="button" class="btn tonal" onclick={() => groupDialog("group-new", "")}><i>add</i><span>Nuevo grupo</span></button>
        <a class="btn outlined" href="/externals/new"><i>public</i><span>Agregar servicio externo</span></a>
        <a class="btn outlined" href="/discovered"><i>travel_explore</i><span>Descubiertos</span></a>
        <a class="btn outlined" href="/hidden"><i>visibility_off</i><span>Ocultos</span></a>
        <a class="btn outlined" href="/export"><i>output</i><span>Exportar a maestro.yaml</span></a>
      </nav>
    {/if}
    <datalist id="group-names">{#each groupNames as n}<option value={n}></option>{/each}</datalist>
  </div>
{/if}

{#if layoutError}
  <div class="snackbar error active" role="alert" transition:fly={{ y: 16, duration: dur("--dur-m"), easing: ease }}>{layoutError}</div>
{/if}

{#if dialog?.kind === "rename"}
  <Modal title="Cambiar nombre" onclose={() => (dialog = null)}>
    {#snippet children(close)}
      <form onsubmit={(e) => submit(e, "/names", close)}>
        <input type="hidden" name="key" value={dialog.key} />
        <div class="field label border">
          <input type="text" name="name" id="rename-name" maxlength="80" autocomplete="off" placeholder=" " value={dialog.name} data-focus />
          <label for="rename-name">Nombre</label>
        </div>
        <p class="small-text secondary-text">Vacío vuelve al nombre original.</p>
        <p class="small-text error-text dialog-error" role="alert">{dialogError}</p>
        <nav class="right-align">
          <button type="button" class="border" onclick={close}>Cancelar</button>
          <button type="submit">Guardar</button>
        </nav>
      </form>
    {/snippet}
  </Modal>
{:else if dialog?.kind === "move"}
  <Modal title="Mover a grupo" onclose={() => (dialog = null)}>
    {#snippet children(close)}
      <form onsubmit={(e) => submit(e, "/groups/move", close)}>
        <input type="hidden" name="key" value={dialog.key} />
        <div class="field label border">
          <input type="text" name="group" id="move-group" maxlength="40" autocomplete="off" placeholder=" " list="group-names" value={dialog.group} data-focus />
          <label for="move-group">Grupo</label>
        </div>
        <p class="small-text secondary-text">Uno que no existe se crea. Vacío va a Otros.</p>
        <p class="small-text error-text dialog-error" role="alert">{dialogError}</p>
        <nav class="right-align wrap">
          <button type="submit" class="transparent" name="original" value="1" title="El que dicen los labels del compose">Grupo original</button>
          <div class="max"></div>
          <button type="button" class="border" onclick={close}>Cancelar</button>
          <button type="submit">Mover</button>
        </nav>
      </form>
    {/snippet}
  </Modal>
{:else if dialog?.kind === "group-new"}
  <Modal title="Nuevo grupo" onclose={() => (dialog = null)}>
    {#snippet children(close)}
      <form onsubmit={(e) => submit(e, "/groups", close)}>
        <div class="field label border">
          <input type="text" name="name" id="group-new-name" maxlength="40" autocomplete="off" placeholder=" " required data-focus />
          <label for="group-new-name">Nombre</label>
        </div>
        <p class="small-text secondary-text">Aparece vacío al final: arrastrá tarjetas o usá "Mover a grupo".</p>
        <p class="small-text error-text dialog-error" role="alert">{dialogError}</p>
        <nav class="right-align">
          <button type="button" class="border" onclick={close}>Cancelar</button>
          <button type="submit">Crear</button>
        </nav>
      </form>
    {/snippet}
  </Modal>
{:else if dialog?.kind === "group-rename"}
  <Modal title="Cambiar nombre del grupo" onclose={() => (dialog = null)}>
    {#snippet children(close)}
      <form onsubmit={(e) => submit(e, "/groups/rename", close)}>
        <input type="hidden" name="name" value={dialog.name} />
        <div class="field label border">
          <input type="text" name="to" id="group-rename-to" maxlength="40" autocomplete="off" placeholder=" " required value={dialog.name} data-focus />
          <label for="group-rename-to">Nombre</label>
        </div>
        <p class="small-text secondary-text">Si ya hay un grupo con ese nombre, los dos quedan en uno.</p>
        <p class="small-text error-text dialog-error" role="alert">{dialogError}</p>
        <nav class="right-align">
          <button type="button" class="border" onclick={close}>Cancelar</button>
          <button type="submit">Guardar</button>
        </nav>
      </form>
    {/snippet}
  </Modal>
{:else if dialog?.kind === "group-delete"}
  <Modal title="Borrar grupo" onclose={() => (dialog = null)}>
    {#snippet children(close)}
      <form onsubmit={(e) => submit(e, "/groups/delete", close)}>
        <input type="hidden" name="name" value={dialog.name} />
        <p>Las tarjetas de <b>{dialog.name}</b> pasan a Otros.</p>
        <p class="small-text error-text dialog-error" role="alert">{dialogError}</p>
        <nav class="right-align">
          <button type="button" class="border" onclick={close}>Cancelar</button>
          <button type="submit" class="error">Borrar</button>
        </nav>
      </form>
    {/snippet}
  </Modal>
{/if}
