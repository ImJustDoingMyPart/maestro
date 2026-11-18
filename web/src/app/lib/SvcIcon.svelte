<!--
  Ícono de un servicio: el logo de la aplicación (internal/icons, con su
  variante oscura si la tiene) o uno de Material Symbols. Es el partial
  "svcicon" de las plantillas.
-->
<script>
  let { logo = null, icon = "deployed_code" } = $props();
</script>

<span class="svc-icon" aria-hidden="true">
  {#if logo}
    {#if logo.SrcDark || logo.srcDark}
      <picture><source media="(prefers-color-scheme: dark)" srcset={logo.SrcDark || logo.srcDark} /><img src={logo.Src || logo.src} alt="" loading="lazy" decoding="async" /></picture>
    {:else}
      <img src={logo.Src || logo.src} alt="" loading="lazy" decoding="async" class:mono={logo.Mono || logo.mono} />
    {/if}
  {:else}
    <i>{icon}</i>
  {/if}
</span>
