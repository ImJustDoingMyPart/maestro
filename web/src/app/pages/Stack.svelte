<!--
  Pestaña Compose de un stack: los botones up/restart/pull/down con su
  salida en vivo y el editor del compose y del .env (web/src/compose.js,
  con CodeMirror, que se carga aparte al entrar acá). El marcado es el que
  espera compose.js; el contenido va en textareas que el editor toma.
  Irse con cambios sin guardar pide confirmación.
-->
<script>
  import { getContext, onMount } from "svelte";

  let { data } = $props();
  const app = getContext("app");
  const st = $derived(data?.Stack);
  const base = (p) => (p || "").split("/").pop();

  let root = $state();
  let editor = null;

  onMount(() => {
    let dead = false;
    const off = app.guard(() => (editor?.dirty() ? "Hay cambios sin guardar en el editor. ¿Irse igual?" : ""));
    if (root) {
      import("../../compose.js").then((m) => {
        if (!dead) editor = new m.Compose(root);
      });
    }
    return () => {
      dead = true;
      off();
      editor?.destroy();
      editor = null;
    };
  });
</script>

{#if st}
  <header class="transparent service-header">
    <nav>
      <a class="button circle transparent" href={st.From ? st.From.Href : "/"} title="Volver" aria-label="Volver"><i>arrow_back</i></a>
      <span class="svc-icon m l"><i>description</i></span>
      <div class="max">
        <h5 class="small">{st.From ? st.From.Name : st.Project}</h5>
        <div class="small-text secondary-text">stack {st.Project} · {st.Host}</div>
      </div>
    </nav>
  </header>

  <div class="tabs left-align">
    {#if st.From}<a href={st.From.Href}><i>history</i><span>Actividad</span></a>{/if}
    <!-- svelte-ignore a11y_missing_attribute -->
    <a class="active" aria-current="page"><i>stacks</i><span>Compose</span></a>
  </div>

  {#if st.Err}
    <article class="small-round no-elevate surface-container-low center-align">
      <p>{st.Err}</p>
      <a class="button" href="/">Volver a Servicios</a>
    </article>
  {:else}
    <section class="compose" bind:this={root} data-base={st.Base} data-run={st.Run} data-run-name={st.RunName} data-busy={st.Busy ? "" : null}>
      <article class="small-round no-elevate surface-container-low stack-actions">
        <nav class="wrap small-space">
          <button class="small" data-action="up" title="docker compose up -d"><i>play_arrow</i><span>Up</span></button>
          <button class="small tonal" data-action="restart" title="docker compose restart"><i>restart_alt</i><span>Restart</span></button>
          <button class="small tonal" data-action="pull" title="docker compose pull"><i>download</i><span>Pull</span></button>
          <button class="small border error-border error-text" data-action="down" title="docker compose down"><i>stop</i><span>Down</span></button>
        </nav>
        <p class="small-text secondary-text no-margin">
          {#if st.Services?.length > 1}
            Los botones actúan sobre el stack entero, que tiene {st.Services.length} servicios:
            {#each st.Services as s, i}{i ? ", " : ""}<a class="link" href={s.Href}>{s.Name}</a>{/each}.
          {:else if !st.Services?.length}
            El stack no tiene contenedores: está bajado o nunca se levantó.
          {:else}
            Los botones actúan sobre el stack {st.Project}.
          {/if}
        </p>
        <p class="small-text no-margin compose-busy" hidden>Hay una operación en curso en este stack.</p>
      </article>

      <section class="log-viewer run-output" hidden>
        <h6 class="small run-title">Salida</h6>
        <nav class="log-toolbar wrap small-space">
          <div class="field small prefix round border no-margin log-search">
            <i>search</i>
            <input type="search" placeholder="Buscar" aria-label="Buscar en la salida" autocomplete="off" />
          </div>
          <!-- svelte-ignore a11y_no_noninteractive_element_to_interactive_role -->
          <nav class="group connected log-levels" role="radiogroup" aria-label="Nivel">
            <button class="left-round small active" data-level="all">Todo</button>
            <button class="no-round small" data-level="warn">Avisos</button>
            <button class="right-round small" data-level="error">Errores</button>
          </nav>
          <span class="chip no-border surface-container log-status" role="status"><span class="dot unknown"></span><span>Conectando…</span></span>
        </nav>
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <div class="log-scroll" tabindex="0" aria-label="Salida">
          <div class="log-spacer"></div>
          <div class="log-window"></div>
        </div>
        <button class="log-bottom extend circle small-elevate" hidden aria-label="Ir al final"><i>arrow_downward</i><span>Ir al final</span></button>
        <dialog class="bottom log-detail">
          <nav>
            <h6 class="max small">Línea</h6>
            <button class="circle transparent" data-copy title="Copiar" aria-label="Copiar"><i>content_copy</i></button>
            <button class="circle transparent" data-close title="Cerrar" aria-label="Cerrar"><i>close</i></button>
          </nav>
          <div class="small-text secondary-text log-detail-meta"></div>
          <pre class="log-detail-text"></pre>
        </dialog>
      </section>

      <div class="tabs left-align file-tabs" role="tablist">
        <!-- svelte-ignore a11y_missing_attribute -->
        <a class="active" data-file="compose" role="tab" aria-selected="true"><span>{base(st.Compose.path)}</span></a>
        <!-- svelte-ignore a11y_missing_attribute -->
        <a data-file="env" role="tab" aria-selected="false"><span>.env</span></a>
      </div>

      <div class="editor-host">
        <textarea class="editor-source" data-file="compose" data-hash={st.Compose.hash} data-path={st.Compose.path} readonly aria-label={base(st.Compose.path)} value={st.Compose.content}></textarea>
        <textarea class="editor-source" data-file="env" data-hash={st.Env.hash} data-path={st.Env.path} readonly hidden aria-label=".env" value={st.Env.content}></textarea>
      </div>

      <nav class="wrap small-space editor-bar">
        <span class="small-text secondary-text max editor-path"></span>
        <span class="small-text secondary-text editor-check" role="status"></span>
        <button class="small transparent" data-revert disabled><i>undo</i><span>Descartar</span></button>
        <button class="small" data-save disabled><i>save</i><span>Guardar</span></button>
      </nav>

      <article class="small-round no-elevate state-reasons issue-box editor-issues" hidden>
        <nav><i class="issues-icon">warning</i><div class="max issues-list"></div></nav>
      </article>

      <article class="small-round no-elevate compose-msg" hidden role="status">
        <nav>
          <i class="msg-icon"></i>
          <div class="max msg-text"></div>
          <button class="circle transparent small" data-dismiss aria-label="Cerrar"><i>close</i></button>
        </nav>
        <pre class="msg-output" hidden></pre>
      </article>

      <dialog class="diff-dialog large-width">
        <h6 class="small">Guardar <span class="diff-file"></span></h6>
        <p class="small-text secondary-text">Antes de escribir se validó con <code>docker compose config</code>. Si la carpeta es un repo de git, se hace un commit solo de este archivo.</p>
        <article class="small-round no-elevate state-reasons issue-box diff-warnings" hidden>
          <nav><i class="issues-icon">warning</i><div class="max issues-list"></div></nav>
        </article>
        <pre class="diff"></pre>
        <nav class="right-align">
          <button class="transparent" data-cancel>Cancelar</button>
          <button data-confirm><i>save</i><span>Guardar</span></button>
        </nav>
      </dialog>

      <dialog class="down-dialog">
        <h6 class="small">¿Bajar el stack {st.Project}?</h6>
        <p>docker compose down detiene y borra los contenedores{st.Services?.length > 1 ? " de los " + st.Services.length + " servicios" : ""}. Los volúmenes y los datos quedan. Se vuelve a levantar con Up.</p>
        <nav class="right-align">
          <button class="transparent" data-cancel>Cancelar</button>
          <button class="error" data-confirm><i>stop</i><span>Down</span></button>
        </nav>
      </dialog>
    </section>
  {/if}
{/if}
