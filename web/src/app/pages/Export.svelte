<!-- Exportar: lo configurado desde la interfaz como YAML para maestro.yaml. -->
<script>
  let { data } = $props();
</script>

<header class="transparent">
  <nav>
    <a class="button circle transparent" href="/" title="Volver" aria-label="Volver a Servicios"><i>arrow_back</i></a>
    <h5 class="max">Exportar a maestro.yaml</h5>
    <a class="button" href="/export.yaml" download><i>download</i><span>Bajar</span></a>
  </nav>
</header>
<p class="secondary-text">Lo configurado desde la interfaz (grupos, nombres, ocultos, stacks y servicios externos) como secciones de <code>maestro.yaml</code>, para versionarlo y rearmar el hub sin perderlo.</p>
<pre class="export-yaml small-round surface-container-low">{data?.Export ?? ""}</pre>
