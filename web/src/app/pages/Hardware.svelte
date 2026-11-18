<!--
  Hardware: un host por vez (?host=), con pestañas para pasar a los demás.
  Se refresca cada 15 segundos (lo que tarda el hub en consultar a los
  agents); los gráficos se refrescan solos, cada minuto.

  Modo edición, por host: arrastrar tarjetas, gráficos y secciones para
  ordenarlos, el ojo para ocultarlos, y arrastrar las pestañas de los
  hosts (el primero es el que se abre al entrar). Cada cambio se guarda
  enseguida. Como en la portada, SortableJS mueve el elemento, al soltar
  se lo devuelve a su lugar y se cambia el estado, que es lo que manda.
-->
<script>
  import { getContext, onMount, tick } from "svelte";
  import { post } from "../lib/api.js";
  import { dur } from "../lib/motion.js";
  import HwCard from "../lib/HwCard.svelte";
  import Chart from "../lib/Chart.svelte";
  import Ranges from "../lib/Ranges.svelte";
  import SvcIcon from "../lib/SvcIcon.svelte";
  import StatePill from "../lib/StatePill.svelte";
  import Eye from "../lib/Eye.svelte";

  let { data, live } = $props();
  const app = getContext("app");

  let local = $state(null);
  let editing = $state(false);
  let dragging = false;
  let flashMsg = $state("");
  $effect(() => {
    const d = data;
    if (!dragging) local = d ? $state.snapshot(d) : null;
  });

  const hosts = $derived(local?.Hardware ?? []);
  const sel = $derived(local?.HWHost ?? "");
  const h = $derived(hosts.find((x) => x.Name === sel));
  const editable = $derived(live && local?.Live);

  onMount(() => {
    // Seguir editando al pasar a otro host desde su pestaña.
    try {
      if (sessionStorage.getItem("maestro.hwEditing")) {
        sessionStorage.removeItem("maestro.hwEditing");
        setEditing(true);
      }
    } catch {}
    const t = setInterval(() => {
      if (!document.hidden && !editing && !document.querySelector("dialog[open]")) app.reload();
    }, 15000);
    const vis = () => !document.hidden && !editing && app.reload();
    document.addEventListener("visibilitychange", vis);
    return () => {
      clearInterval(t);
      document.removeEventListener("visibilitychange", vis);
      stopSorting();
      document.body.classList.remove("editing");
    };
  });

  function setEditing(on) {
    editing = on;
    document.body.classList.toggle("editing", on);
    if (!on) app.reload();
  }
  function tabClick() {
    if (!editing) return;
    try {
      sessionStorage.setItem("maestro.hwEditing", "1");
    } catch {}
  }

  let flashTimer;
  function flash(msg) {
    flashMsg = msg;
    clearTimeout(flashTimer);
    flashTimer = setTimeout(() => (flashMsg = ""), 5000);
  }

  async function send(url, fields) {
    const err = await post(url, { host: sel, ...fields });
    if (err) {
      flash(err);
      app.reload(); // deshace lo que no se guardó
    }
  }

  // El ojo: oculta o muestra un elemento (una tarjeta, un gráfico o una
  // sección) de una lista del host.
  function toggle(list, i) {
    const it = list[i];
    it.Hidden = !it.Hidden;
    send("/hardware/hidden", { item: it.ID, hidden: it.Hidden ? "1" : "0" });
  }

  // ---------- Arrastrar ----------
  let sortables = [];
  let gen = 0;
  function stopSorting() {
    gen++;
    sortables.forEach((s) => s.destroy());
    sortables = [];
  }

  // lists son las listas que se ordenan, por el atributo data-hw-sort.
  function listFor(name) {
    switch (name) {
      case "sections":
        return h.Sections;
      case "storage":
        return h.Storage;
      case "tiles":
        return h.Tiles;
      case "charts":
        return h.Charts;
      case "guests":
        return h.GuestCards;
      case "guestcharts":
        return h.GuestCharts;
    }
    return null;
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
      filter: "[data-hw-hide], [data-icon-pick]",
      preventOnFilter: false,
      ghostClass: "drag-ghost",
      chosenClass: "drag-chosen",
    };
    const sortable = (el, extra, onMove) => {
      let next = null;
      sortables.push(
        Sortable.create(el, {
          ...common,
          ...extra,
          onStart: (e) => {
            dragging = true;
            next = e.item.nextSibling;
          },
          onEnd: (e) => {
            e.from.insertBefore(e.item, next); // la página vuelve a como la dejó Svelte
            dragging = false;
            if (e.oldDraggableIndex !== e.newDraggableIndex) onMove(e.oldDraggableIndex, e.newDraggableIndex);
          },
        }),
      );
    };
    for (const el of document.querySelectorAll(".hw-host [data-hw-sort]")) {
      const name = el.dataset.hwSort;
      const extra = name === "sections" ? { draggable: ".hw-section", handle: "[data-hw-handle]" } : { draggable: "[data-hw-item]" };
      sortable(el, extra, (from, to) => {
        const list = listFor(name);
        if (!list) return;
        const [it] = list.splice(from, 1);
        list.splice(to, 0, it);
        send("/hardware/order", { item: list.map((x) => x.ID) });
      });
    }
    const tabs = document.querySelector("[data-hw-hosts]");
    if (tabs) {
      sortable(tabs, { draggable: ".host-tab" }, (from, to) => {
        const [it] = local.Hardware.splice(from, 1);
        local.Hardware.splice(to, 0, it);
        post("/hardware/hosts", { host: local.Hardware.map((x) => x.Name) }).then((err) => err && flash(err));
      });
    }
  }
  $effect(() => {
    if (editing && h) startSorting();
    else stopSorting();
  });

  const plural = (n, one, many) => (n === 1 ? one : many);
</script>

<header class="page-head">
  <h1 class="page-title">Hardware</h1>
  <div class="page-actions">
    <Ranges />
    {#if editable}
      <button type="button" class="btn outlined edit-toggle" aria-pressed={editing} title="Ordenar y ocultar lo que se ve de este host" onclick={() => setEditing(!editing)}>
        {#if editing}<i>check</i><span class="m l">Listo</span>{:else}<i>edit</i><span class="m l">Editar</span>{/if}
      </button>
    {/if}
  </div>
</header>

{#if local && !hosts.length}
  <article class="empty-state">
    <i>dns</i>
    <p>No hay hosts con agent en maestro.yaml.</p>
  </article>
{/if}

{#if hosts.length > 1}
  <nav class="host-tabs" aria-label="Hosts" data-hw-hosts>
    {#each hosts as x (x.Name)}
      <a class="host-tab" class:active={x.Name === sel} href={"/hardware?host=" + encodeURIComponent(x.Name)} data-host={x.Name} aria-current={x.Name === sel ? "page" : null} onclick={tabClick}>
        <span class="dot {x.OK ? 'up' : 'down'}"></span><span>{x.Name}</span>
      </a>
    {/each}
  </nav>
{/if}

{#if h}
  <section class="hw-host" data-hw-host={h.Name}>
    <p class="edit-only edit-hint">
      <i>info</i><span
        >Arrastrá las tarjetas, los gráficos y las secciones (desde <i class="inline">drag_indicator</i>) para ordenarlos, y usá el ojo para ocultarlos; vale solo para {h.Name}.{#if hosts.length > 1}
          Las pestañas de los hosts también se arrastran: el primero es el que se abre al entrar.{/if}</span
      >
    </p>
    <div class="hw-host-head">
      <span class="svc-icon"><i>dns</i></span>
      <div class="hw-host-text">
        <div class="hw-host-name">{h.Name}{#if h.Info.hostname && h.Info.hostname !== h.Name}{" "}<span>{h.Info.hostname}</span>{/if}</div>
        <div class="hw-host-sub">
          {#if h.Info.cpu_model}<span>{h.Info.cpu_model}</span>{/if}
          {#if h.Uptime}<span>encendido hace {h.Uptime}</span>{/if}
        </div>
      </div>
      {#if !h.OK}<StatePill state="down" label="Sin respuesta" />{/if}
    </div>

    {#if h.NoSystem}
      <article class="empty-state">
        <i>update</i>
        <p>El agent de {h.Name} no manda métricas: es de una versión anterior. Actualizalo para ver su hardware.</p>
      </article>
    {:else if h.Waiting}
      <article class="empty-state">
        <i>hourglass_top</i>
        <p>El agent recién arranca: las métricas aparecen con la segunda muestra (unos 15 segundos).</p>
      </article>
    {/if}

    <div class="hw-sections" data-hw-sort="sections">
      {#each h.Sections ?? [] as sec, si (sec.ID)}
        <div class="hw-section" class:is-hidden={sec.Hidden} data-hw-item={sec.ID}>
          <h2 class="section-title">
            <i class="edit-only hw-handle" data-hw-handle title="Arrastrar la sección">drag_indicator</i><span>{sec.Title}</span>
            <Eye hidden={sec.Hidden} onclick={() => toggle(h.Sections, si)} />
          </h2>
          {#if sec.ID === "section:storage"}
            <div class="hw-cards balanced" data-hw-sort="storage">
              {#each h.Storage ?? [] as c, i (c.ID || c.Title)}<HwCard {c} ontoggle={() => toggle(h.Storage, i)} />{/each}
            </div>
            {#if h.FSChart}<div class="hw-charts"><Chart spec={h.FSChart} /></div>{/if}
          {:else if sec.ID === "section:tiles"}
            <div class="hw-tiles balanced" data-hw-sort="tiles">
              {#each h.Tiles ?? [] as c, i (c.ID || c.Title)}<HwCard {c} ontoggle={() => toggle(h.Tiles, i)} />{/each}
            </div>
          {:else if sec.ID === "section:charts"}
            <div class="hw-charts" data-hw-sort="charts">
              {#each h.Charts ?? [] as c, i (c.ID || c.Title)}<Chart spec={c} ontoggle={() => toggle(h.Charts, i)} />{/each}
            </div>
          {:else if sec.ID === "section:guests"}
            <div class="hw-cards balanced" data-hw-sort="guests">
              {#each h.GuestCards ?? [] as c, i (c.ID || c.Title)}<HwCard {c} ontoggle={() => toggle(h.GuestCards, i)} />{/each}
            </div>
            <div class="hw-charts" data-hw-sort="guestcharts">
              {#each h.GuestCharts ?? [] as c, i (c.ID || c.Title)}<Chart spec={c} ontoggle={() => toggle(h.GuestCharts, i)} />{/each}
            </div>
          {:else if sec.ID === "section:containers"}
            <div class="hw-table">
              <div class="hw-row hw-row-head"><span>Stack o contenedor</span><span>CPU</span><span class="m l">Red</span><span>Memoria</span></div>
              {#each h.Containers ?? [] as r (r.Name)}
                {#if r.Members?.length}
                  <details class="hw-stack" data-stack={r.Name}>
                    <summary class="hw-row" title={"Ver sus " + r.Members.length + " contenedores"}>
                      <span class="hw-row-name"><SvcIcon logo={r.Logo} icon={r.Icon} /><span title={r.Name}>{r.Name}</span><span class="hw-count">{r.Members.length}</span><i class="hw-chevron">expand_more</i></span>
                      <span>{r.CPU}</span>
                      <span class="m l hw-net"><i title="Recibe">south</i>{r.NetRx}<i title="Envía">north</i>{r.NetTx}</span>
                      <span>{r.Mem}</span>
                    </summary>
                    {#each r.Members as m (m.Name)}
                      <svelte:element this={m.Href ? "a" : "div"} class="hw-row hw-member" href={m.Href || null}>
                        <span class="hw-row-name"><span title={m.Name}>{m.Name}</span></span>
                        <span>{m.CPU}</span>
                        <span class="m l hw-net"><i title="Recibe">south</i>{m.NetRx}<i title="Envía">north</i>{m.NetTx}</span>
                        <span>{m.Mem}</span>
                      </svelte:element>
                    {/each}
                    <a class="hw-row hw-member hw-stack-link" href={r.Href}><span class="hw-row-name"><i>description</i><span>Abrir el stack</span></span></a>
                  </details>
                {:else}
                  <svelte:element this={r.Href ? "a" : "div"} class="hw-row" href={r.Href || null}>
                    <span class="hw-row-name"><SvcIcon logo={r.Logo} icon={r.Icon} /><span title={r.Name}>{r.Name}</span></span>
                    <span>{r.CPU}</span>
                    <span class="m l hw-net"><i title="Recibe">south</i>{r.NetRx}<i title="Envía">north</i>{r.NetTx}</span>
                    <span>{r.Mem}</span>
                  </svelte:element>
                {/if}
              {/each}
              {#if h.MoreCount}<div class="hw-row hw-row-more"><span>Y {h.MoreCount} más, que usan menos memoria.</span></div>{/if}
            </div>
          {/if}
        </div>
      {/each}
    </div>

    {#if h.Errors?.length}
      <details class="hw-errors">
        <summary>El agent avisó {h.Errors.length} {plural(h.Errors.length, "problema", "problemas")} al leer</summary>
        <ul>{#each h.Errors as e}<li>{e}</li>{/each}</ul>
      </details>
    {/if}
  </section>
{/if}

{#if flashMsg}
  <div class="snackbar error active" role="alert">{flashMsg}</div>
{/if}
