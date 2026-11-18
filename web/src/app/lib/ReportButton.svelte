<!--
  Botón "Copiar informe" de la página del servicio: pide el informe a la
  API (con la sesión) y lo copia al portapapeles. Si no se puede copiar
  (sin HTTPS el navegador no da el portapapeles, o lo niega), lo muestra
  en un diálogo para copiarlo a mano.
-->
<script>
  import Modal from "./Modal.svelte";

  let { url } = $props();
  let busy = $state(false);
  let label = $state("");
  let icon = $state("");
  let shown = $state(null); // el texto, si no se pudo copiar

  async function fetchReport() {
    const r = await fetch(url, { credentials: "same-origin" });
    if (!r.ok) throw new Error("el hub respondió " + r.status);
    return r.text();
  }

  // copy empieza a escribir en el portapapeles dentro del clic: Safari
  // exige que el pedido salga del gesto, así que el texto va como promesa
  // dentro del ClipboardItem en vez de esperarlo antes.
  function copy(text) {
    if (!window.isSecureContext || !navigator.clipboard) return Promise.reject(new Error("sin portapapeles"));
    if (window.ClipboardItem && navigator.clipboard.write) {
      const blob = text.then((t) => new Blob([t], { type: "text/plain" }));
      return navigator.clipboard.write([new ClipboardItem({ "text/plain": blob })]);
    }
    return text.then((t) => navigator.clipboard.writeText(t));
  }

  // within corta una promesa que no termina: si el navegador deja colgado
  // el permiso del portapapeles, el botón no puede quedar apagado.
  const within = (p, ms) => Promise.race([p, new Promise((_, rej) => setTimeout(() => rej(new Error("el portapapeles no respondió")), ms))]);

  function flash(l, i) {
    label = l;
    icon = i;
    setTimeout(() => (label = icon = ""), 2000);
  }

  async function onclick() {
    busy = true;
    const text = fetchReport();
    try {
      await within(copy(text), 3000);
      flash("Copiado", "check");
    } catch {
      try {
        shown = await text;
      } catch (err) {
        console.error("informe:", err);
        flash("No se pudo", "error");
      }
    } finally {
      busy = false;
    }
  }

  function select(node) {
    node.focus();
    node.select();
  }
</script>

<button class="btn outlined" disabled={busy} title="Copia un resumen en Markdown (estado, checks, logs y compose, con los secretos censurados) para pegarlo en un agente de IA" {onclick}>
  <i>{icon || "content_copy"}</i>
  <span class="m l">{label || "Copiar informe"}</span>
</button>

{#if shown !== null}
  <Modal title="Informe para IA" class="report-dialog" onclose={() => (shown = null)}>
    {#snippet children(close)}
      <p class="small-text secondary-text">El navegador no dejó copiarlo solo (sin HTTPS no da el portapapeles). Está seleccionado: copialo con Ctrl+C o con el menú.</p>
      <div class="field textarea border extra">
        <textarea readonly aria-label="Informe" value={shown} use:select></textarea>
      </div>
      <nav class="right-align">
        <a class="button border" href={url} target="_blank" rel="noopener">Abrir como texto</a>
        <button type="button" onclick={close}>Cerrar</button>
      </nav>
    {/snippet}
  </Modal>
{/if}
