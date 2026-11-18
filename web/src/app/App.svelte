<!--
  La aplicación entera: la barra de arriba (o la de abajo en el celular) y
  la página que corresponde a la URL. Navegar no recarga: un link propio
  cambia la URL con history.pushState, se piden los datos de la página
  nueva (la misma URL con Accept: application/json) y, mientras llegan,
  queda la anterior. Las páginas que no son de la aplicación (el login,
  bajar un archivo) se cargan como siempre.
-->
<script>
  import { onMount, setContext, tick } from "svelte";
  import { fade } from "svelte/transition";
  import { getPage } from "./lib/api.js";
  import { dur, ease } from "./lib/motion.js";
  import IconDialog from "./lib/IconDialog.svelte";
  import Portada from "./pages/Portada.svelte";
  import Hardware from "./pages/Hardware.svelte";
  import Service from "./pages/Service.svelte";
  import Stack from "./pages/Stack.svelte";
  import Discovered from "./pages/Discovered.svelte";
  import Hidden from "./pages/Hidden.svelte";
  import External from "./pages/External.svelte";
  import Export from "./pages/Export.svelte";
  import Muestrario from "./pages/Muestrario.svelte";

  let { demo = false } = $props();

  // Cada ruta: la expresión, el componente y si pide sus datos (la portada
  // los pide ella, a /api/portada).
  const routes = [
    { re: /^\/$/, page: Portada, own: true, nav: "services" },
    { re: /^\/hardware$/, page: Hardware, nav: "hardware" },
    { re: /^\/services\/(.+)$/, page: Service, nav: "services" },
    { re: /^\/metrics\/(.+)$/, page: Service, nav: "services" },
    { re: /^\/stacks\/[^/]+\/[^/]+$/, page: Stack, nav: "services" },
    { re: /^\/discovered$/, page: Discovered, nav: "services" },
    { re: /^\/hidden$/, page: Hidden, nav: "services" },
    { re: /^\/externals\/[^/]+$/, page: External, nav: "services" },
    { re: /^\/export$/, page: Export, nav: "services" },
    { re: /^\/muestrario$/, page: Muestrario, nav: "" },
  ];
  const match = (path) => routes.find((r) => r.re.test(path));

  // current es lo que se ve: la ruta, su URL y sus datos.
  let current = $state(null);
  let loadError = $state("");
  let seq = 0; // descarta una carga vieja si llegó otra navegación
  let abort = null;

  // Antes de irse, cada página puede pedir confirmación (cambios sin
  // guardar en el editor de Compose): guard devuelve el texto o "".
  const guards = new Set();

  async function load(url, { quiet = false } = {}) {
    const u = new URL(url, location.href);
    const route = match(u.pathname);
    if (!route) {
      location.href = url;
      return;
    }
    const mine = ++seq;
    if (route.own) {
      abort?.abort();
      current = { route, url: u.pathname + u.search, data: null, status: 200 };
      loadError = "";
      document.title = "Servicios · Maestro";
      return;
    }
    abort?.abort();
    const ac = new AbortController();
    abort = ac;
    try {
      const { status, data } = await getPage(u.pathname + u.search, ac.signal);
      if (mine !== seq) return;
      current = { route, url: u.pathname + u.search, data, status };
      loadError = "";
      if (data?.Title) document.title = data.Title + " · Maestro";
    } catch (e) {
      if (mine !== seq || e.name === "AbortError") return;
      if (quiet) return; // un refresco que falla deja lo que había
      loadError = e.message;
      if (!current) current = { route, url: u.pathname + u.search, data: null, status: 0 };
    }
  }

  // navigate va a otra página de la aplicación.
  async function navigate(url, { replace = false } = {}) {
    for (const g of guards) {
      const msg = g();
      if (msg && !confirm(msg)) return;
    }
    const st = { ...(history.state || {}), scroll: window.scrollY };
    history.replaceState(st, "");
    if (replace) history.replaceState({}, "", url);
    else history.pushState({}, "", url);
    await load(url);
    await tick();
    if (!replace) window.scrollTo(0, 0);
  }

  // reload vuelve a pedir los datos de la página que se ve, sin indicador.
  function reload() {
    if (!current) return;
    // La portada pide sus datos ella: se le avisa.
    if (current.route.own) window.dispatchEvent(new Event("maestro:refresh"));
    else return load(current.url, { quiet: true });
  }

  setContext("app", {
    navigate,
    reload,
    guard(fn) {
      guards.add(fn);
      return () => guards.delete(fn);
    },
    get demo() {
      return demo;
    },
  });

  // Los links propios no recargan. Se dejan pasar los que abren otra
  // pestaña, bajan algo o van a una página que no es de la aplicación.
  function onclick(e) {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const a = e.target.closest("a[href]");
    if (!a || a.target || a.hasAttribute("download")) return;
    const u = new URL(a.href, location.href);
    if (u.origin !== location.origin || !match(u.pathname)) return;
    e.preventDefault();
    if (u.pathname + u.search === location.pathname + location.search && !u.hash) return;
    navigate(u.pathname + u.search);
  }

  async function onpopstate(e) {
    await load(location.pathname + location.search);
    await tick();
    window.scrollTo(0, e.state?.scroll ?? 0);
  }

  onMount(() => {
    history.scrollRestoration = "manual";
    load(location.pathname + location.search);
  });

  const active = $derived(current?.route.nav ?? "services");
  const Page = $derived(current?.route.page);
</script>

<svelte:document {onclick} />
<svelte:window {onpopstate} />

{#snippet logout()}
  <form method="post" action="/logout">
    <button class="icon-btn" title="Salir" aria-label="Salir"><i>logout</i></button>
  </form>
{/snippet}

<header class="topbar m l">
  <a class="brand" href="/"><span class="brand-mark"><i>hub</i></span><span class="brand-name">Maestro</span></a>
  <nav class="topnav" aria-label="Secciones">
    <a class="topnav-item" class:active={active === "services"} href="/" aria-current={active === "services" ? "page" : null}>
      <i>dashboard</i><span>Servicios</span>
    </a>
    <a class="topnav-item" class:active={active === "hardware"} href="/hardware" aria-current={active === "hardware" ? "page" : null}>
      <i>memory</i><span>Hardware</span>
    </a>
  </nav>
  <div class="max"></div>
  {#if !demo}{@render logout()}{/if}
</header>

<nav class="bottom s">
  <a href="/" class:active={active === "services"}><i>dashboard</i><span>Servicios</span></a>
  <a href="/hardware" class:active={active === "hardware"}><i>memory</i><span>Hardware</span></a>
</nav>

<main class="responsive">
  {#if loadError}
    <article class="banner error">
      <i>cloud_off</i>
      <div class="banner-text"><div class="banner-title">{loadError}</div><div class="banner-sub">Probá de nuevo en un rato.</div></div>
    </article>
  {/if}
  {#if Page}
    {#key current.url.split("?")[0]}
      <div class="app-page" in:fade={{ duration: dur("--dur-s"), easing: ease }}>
        <Page data={current.data} status={current.status} url={current.url} live={!demo} {demo} />
      </div>
    {/key}
  {/if}
</main>

<IconDialog />
