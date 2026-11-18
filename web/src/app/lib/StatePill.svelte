<!--
  Estado: el punto y la palabra, en el color del estado. Es la forma única
  de mostrar un estado con texto.
-->
<script>
  let { state, label, title = null } = $props();
</script>

<span class="state-pill {state}" {title}><span class="dot {state}"></span>{label}</span>
