<!--
  Alta o edición de un servicio externo (algo que no se descubre y se
  vigila con un check HTTP o TCP). Al guardar vuelve a la portada; si el
  hub rechaza algo, los errores vuelven en el JSON de la página.
-->
<script>
  import { getContext } from "svelte";
  import { submit } from "../lib/api.js";

  let { data } = $props();
  const app = getContext("app");

  let f = $state(null);
  let errors = $state([]);
  let saving = $state(false);
  $effect(() => {
    const e = data?.Ext;
    f = e ? { ...e } : null;
    errors = e?.Errors ?? [];
  });

  async function save(e) {
    e.preventDefault();
    saving = true;
    const fields = {
      name: f.Name, group: f.Group, url: f.URL, description: f.Description, kind: f.Kind,
      target: f.Target, expect: f.Expect, interval: f.Interval, timeout: f.Timeout,
    };
    if (f.Redirects) fields.redirects = "1";
    const r = await submit(f.ID ? "/externals/" + encodeURIComponent(f.ID) : "/externals", fields);
    saving = false;
    if (r.ok) return app.navigate("/");
    errors = r.data?.Ext?.Errors ?? [r.error];
  }

  async function remove(e) {
    e.preventDefault();
    saving = true;
    const r = await submit("/externals/" + encodeURIComponent(f.ID) + "/delete", {});
    saving = false;
    if (r.ok) return app.navigate("/");
    errors = [r.error];
  }
</script>

{#if f}
  <header class="transparent">
    <nav>
      <a class="button circle transparent" href="/" title="Volver" aria-label="Volver a Servicios"><i>arrow_back</i></a>
      <h5 class="max">{f.ID ? "Editar servicio externo" : "Nuevo servicio externo"}</h5>
    </nav>
  </header>
  <p class="secondary-text">Algo que Maestro no puede descubrir (el router, un sitio web) y se vigila con un check. El check push va solo en <code>maestro.yaml</code>: su token es un secreto.</p>

  {#if errors.length}
    <article class="small-round no-elevate error-container" role="alert">
      <ul class="form-errors">{#each errors as e}<li>{e}</li>{/each}</ul>
    </article>
  {/if}

  <form class="external-form" onsubmit={save}>
    <div class="grid">
      <div class="s12 m6 field label border">
        <input type="text" id="ext-name" bind:value={f.Name} maxlength="80" required autocomplete="off" placeholder=" " />
        <label for="ext-name">Nombre</label>
      </div>
      <div class="s12 m6 field label border">
        <input type="text" id="ext-group" bind:value={f.Group} maxlength="40" list="ext-groups" autocomplete="off" placeholder=" " />
        <label for="ext-group">Grupo (vacío: Otros)</label>
        <datalist id="ext-groups">{#each f.Groups ?? [] as g}<option value={g}></option>{/each}</datalist>
      </div>
      <div class="s12 m6 field label border">
        <input type="url" id="ext-url" bind:value={f.URL} autocomplete="off" placeholder=" " />
        <label for="ext-url">Link de la tarjeta (opcional)</label>
      </div>
      <div class="s12 m6 field label border">
        <input type="text" id="ext-desc" bind:value={f.Description} maxlength="200" autocomplete="off" placeholder=" " />
        <label for="ext-desc">Descripción (opcional)</label>
      </div>
    </div>

    <h6 class="small">Check</h6>
    <nav class="check-kind">
      <label class="radio"><input type="radio" name="kind" value="http" bind:group={f.Kind} /><span>HTTP</span></label>
      <label class="radio"><input type="radio" name="kind" value="tcp" bind:group={f.Kind} /><span>TCP</span></label>
    </nav>
    <div class="grid">
      <div class="s12 field label border">
        <input type="text" id="ext-target" bind:value={f.Target} required autocomplete="off" placeholder=" " />
        <label for="ext-target">{f.Kind === "tcp" ? "host:puerto (192.168.0.1:53)" : "URL (http://192.168.0.1/)"}</label>
      </div>
      {#if f.Kind !== "tcp"}
        <div class="s12 m6 field label border">
          <input type="text" id="ext-expect" bind:value={f.Expect} inputmode="numeric" maxlength="3" autocomplete="off" placeholder=" " />
          <label for="ext-expect">Código esperado (vacío: cualquier 2xx)</label>
        </div>
        <div class="s12 m6 redirects">
          <label class="checkbox"><input type="checkbox" bind:checked={f.Redirects} /><span>Seguir redirecciones</span></label>
        </div>
      {/if}
      <div class="s6 field label border">
        <input type="text" id="ext-interval" bind:value={f.Interval} autocomplete="off" placeholder=" " />
        <label for="ext-interval">Cada cuánto (vacío: 60s)</label>
      </div>
      <div class="s6 field label border">
        <input type="text" id="ext-timeout" bind:value={f.Timeout} autocomplete="off" placeholder=" " />
        <label for="ext-timeout">Timeout (vacío: 10s)</label>
      </div>
    </div>
    <nav class="right-align">
      <a class="button border" href="/">Cancelar</a>
      <button type="submit" disabled={saving}>{f.ID ? "Guardar" : "Crear"}</button>
    </nav>
  </form>

  {#if f.ID}
    <details class="danger-zone">
      <summary class="small-text secondary-text">Borrar este servicio</summary>
      <form onsubmit={remove}>
        <p class="small-text">Se deja de vigilar y desaparece de la portada. Su historial de checks se va borrando con la compactación.</p>
        <button class="error small" disabled={saving}><i>delete</i><span>Borrar {f.Name}</span></button>
      </form>
    </details>
  {/if}
{:else if data}
  <article class="small-round no-elevate surface-container-low center-align">
    <h6>Ese servicio externo no existe</h6>
    <a class="button" href="/">Volver a Servicios</a>
  </article>
{/if}
