<!-- Botón del ojo del modo edición: oculta o muestra un elemento. -->
<script>
  let { hidden, onclick } = $props();
</script>

<button type="button" class="icon-btn small edit-only hw-eye" data-hw-hide aria-pressed={hidden} title="Ocultar o mostrar" {onclick}>
  <i class="when-shown">visibility</i><i class="when-hidden">visibility_off</i>
</button>
