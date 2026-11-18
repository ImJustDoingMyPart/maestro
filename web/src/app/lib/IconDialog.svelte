<!--
  Diálogo "Cambiar ícono", uno para toda la aplicación. Lo abre cualquier
  botón con data-icon-pick (data-key, data-icon, data-name): busca en las
  colecciones mientras se escribe y guarda con POST /icons/set. Después de
  guardar, la página que se ve vuelve a pedir sus datos.
-->
<script>
  import { getContext } from "svelte";
  import { getJSON, post } from "./api.js";
  import Modal from "./Modal.svelte";

  const app = getContext("app");

  let target = $state(null); // { key, name }
  let value = $state("");
  let hits = $state([]);
  let searched = $state(false);
  let error = $state("");
  let saving = $state(false);
  let seq = 0;
  let timer = 0;

  function onclick(e) {
    const b = e.target.closest("[data-icon-pick]");
    if (!b || b.disabled) return;
    e.preventDefault();
    target = { key: b.dataset.key, name: b.dataset.name || "" };
    value = b.dataset.icon || "";
    error = "";
    hits = [];
    searched = false;
    // Sin ícono elegido, busca por el nombre sin ".service" ni ".timer".
    search(value || target.name.replace(/\.(service|timer)$/, ""));
  }

  async function search(q) {
    const mine = ++seq;
    if (!q.trim()) {
      hits = [];
      searched = false;
      return;
    }
    try {
      const res = await getJSON("/icons/search?q=" + encodeURIComponent(q));
      if (mine !== seq) return; // llegó una búsqueda más nueva
      hits = res;
      searched = true;
    } catch {
      /* sin conexión: quedan los de antes */
    }
  }

  function oninput() {
    clearTimeout(timer);
    timer = setTimeout(() => search(value), 150);
  }

  async function save(close, icon) {
    saving = true;
    error = await post("/icons/set", { key: target.key, icon, back: location.pathname });
    saving = false;
    if (error) return;
    close();
    app.reload();
  }
</script>

<svelte:document {onclick} />

{#if target}
  <Modal title={"Ícono de " + target.name} class="icon-dialog" onclose={() => (target = null)}>
    {#snippet children(close)}
      <form
        onsubmit={(e) => {
          e.preventDefault();
          save(close, value.trim());
        }}
      >
        <div class="field label prefix border">
          <i>search</i>
          <input type="text" id="icon-input" bind:value {oninput} maxlength="400" autocomplete="off" spellcheck="false" placeholder=" " data-focus />
          <label for="icon-input">Buscar o escribir el nombre</label>
        </div>
        <div class="icon-results">
          {#each hits as h (h.spec)}
            <button type="button" class="icon-choice" class:selected={h.spec === value.trim()} title={h.title + " (" + h.spec + ")"} onclick={() => (value = h.spec)}>
              <span class="svc-icon">
                {#if h.srcDark}
                  <picture><source media="(prefers-color-scheme: dark)" srcset={h.srcDark} /><img src={h.src} alt="" loading="lazy" /></picture>
                {:else}
                  <img src={h.src} alt="" loading="lazy" />
                {/if}
              </span>
              <span class="icon-choice-label">{h.title}</span>
            </button>
          {/each}
          {#if searched && !hits.length}
            <p class="icon-empty">Nada con ese nombre en las colecciones. Se guarda igual tal como está escrito.</p>
          {/if}
        </div>
        <p class="small-text secondary-text">De dashboard-icons y selfh.st. También vale <code>mdi-…</code>, <code>si-…</code>, un ícono de Material Symbols o una URL.</p>
        {#if error}<p class="small-text error-text dialog-error" role="alert">{error}</p>{/if}
        <nav class="dialog-actions">
          <button type="button" class="btn text" title="Vuelve al ícono que adivina el hub" disabled={saving} onclick={() => save(close, "")}>Automático</button>
          <div class="max"></div>
          <button type="button" class="btn outlined" onclick={close}>Cancelar</button>
          <button type="submit" class="btn filled" disabled={saving}>Guardar</button>
        </nav>
      </form>
    {/snippet}
  </Modal>
{/if}
