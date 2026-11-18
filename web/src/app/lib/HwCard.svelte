<!--
  Tarjeta de Hardware (hwCard de internal/hub/hardware.go): mosaicos,
  almacenamiento, discos, LXC y VMs, con las mismas partes en el mismo
  lugar.
-->
<script>
  import SvcIcon from "./SvcIcon.svelte";
  import StatePill from "./StatePill.svelte";
  import Eye from "./Eye.svelte";

  let { c, ontoggle = null } = $props();
</script>

<article class="hw-tile" class:warn={c.Warn} class:is-hidden={c.Hidden} data-hw-item={c.ID || null} data-state={c.State || null}>
  <div class="hw-label">
    <SvcIcon logo={c.Logo} icon={c.Icon} />
    <span class="hw-title" title={c.Title}>{#if c.Href}<a class="stretched" href={c.Href}>{c.Title}</a>{:else}{c.Title}{/if}</span>
    {#if c.Aside}<span class="hw-aside">{c.Aside}</span>{/if}
    {#if c.StateLabel}<StatePill state={c.State} label={c.StateLabel} title={c.StateTitle || null} />{/if}
    {#if c.Key}
      <button type="button" class="icon-btn small edit-only above" data-icon-pick data-key={c.Key} data-icon={c.IconSet} data-name={c.Title} title="Cambiar ícono" aria-label={"Cambiar el ícono de " + c.Title}><i>imagesmode</i></button>
    {/if}
    {#if c.ID && ontoggle}<Eye hidden={c.Hidden} onclick={ontoggle} />{/if}
  </div>
  <div class="hw-value">{c.Value}{#if c.Unit}<small>{c.Unit}</small>{/if}</div>
  <div class="meter" class:empty={!c.HasBar}><span style:inline-size={c.Pct.toFixed(1) + "%"}></span></div>
  <div class="hw-sub" title={c.Sub}>{c.Sub}</div>
</article>
