// Visor de logs: el de la pestaña Actividad y el de la salida de las
// acciones de compose.
//
// Recibe las líneas por Server-Sent Events (EventSource nativo) y las
// dibuja con scroll virtual: el contenedor tiene la altura de todas las
// líneas, pero en el DOM solo están las que se ven más un margen. Así un
// contenedor que escribe miles de líneas no traba el navegador del celular.
//
// Las líneas no se parten (white-space: pre) para que todas midan lo mismo:
// con altura fija, la fila i está en i * alto y no hace falta medir nada.
// Una línea larga se lee entera tocándola.

import { LogBuffer } from "./buffer.js";
import { detectLevel, stripAnsi, INFO, WARN, ERROR } from "./level.js";

const CAP = 10000; // líneas que se guardan
// En pausa (el usuario subió a leer) el buffer puede crecer hasta acá antes
// de descartar: si no, con un contenedor que escribe mucho, lo que se está
// leyendo se borraría en segundos. Al volver al final se recorta a CAP.
const PAUSED_CAP = 50000;
const OVERSCAN = 20; // filas de más arriba y abajo de lo visible
const MAX_PENDING = CAP; // si la pestaña está oculta, lo que espera dibujarse

// Con prefijo: "error" a secas es una clase de color de Beer CSS.
const LEVEL_CLASS = ["", "lvl-warn", "lvl-error"];
const MIN_LEVEL = { all: INFO, warn: WARN, error: ERROR };

const timeFmt = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
});
const fullFmt = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "medium",
  hour12: false,
});

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// Viewer es el visor. Opciones:
//   autoconnect: false para no conectarse hasta llamar a open(url).
//   fit: false para no estirarlo hasta el borde de la pantalla (la altura
//        sale del CSS).
//   onEnd(data): qué hacer con el evento "end" en vez de lo de los logs; el
//        EventSource ya está cerrado. Devuelve [punto, texto] del estado.
export class Viewer {
  constructor(root, opts = {}) {
    this.root = root;
    this.opts = { autoconnect: true, fit: true, ...opts };
    this.streamURL = root.dataset.stream;
    this.replicas = root.dataset.replicas ? root.dataset.replicas.split(" ") : [];
    this.scroll = root.querySelector(".log-scroll");
    this.spacer = root.querySelector(".log-spacer");
    this.win = root.querySelector(".log-window");
    this.bottomBtn = root.querySelector(".log-bottom");
    this.statusEl = root.querySelector(".log-status");
    this.dialog = document.querySelector(".log-detail");

    this.buf = new LogBuffer(CAP);
    this.pending = [];
    this.follow = true; // pegado al final
    this.unseen = 0; // líneas que llegaron sin estar pegado al final
    this.query = "";
    this.minLevel = INFO;
    this.container = ""; // réplica elegida; "" = todas
    this.rows = []; // filas del DOM, reusadas entre dibujos
    this.frame = 0;

    this.lineH = this.measureLine();
    this.bind();
    this.fitHeight();
    if (this.opts.autoconnect) this.connect();
  }

  // open conecta el visor a otro stream, desde cero.
  open(url) {
    this.streamURL = url;
    this.lineH = this.measureLine(); // si estaba oculto, medía 0
    this.fitHeight();
    this.connect();
  }

  // measureLine mide una fila de prueba: el alto sale del CSS (fuente y
  // line-height), no de una constante que se desfase.
  measureLine() {
    const probe = el("div", "log-line", "M");
    this.win.append(probe);
    const h = probe.getBoundingClientRect().height || 20;
    probe.remove();
    return h;
  }

  bind() {
    this.scroll.addEventListener("scroll", () => {
      this.follow = this.atBottom();
      if (this.follow) this.unseen = 0;
      this.schedule();
    }, { passive: true });

    this.bottomBtn.addEventListener("click", () => this.toBottom());
    this.scroll.addEventListener("keydown", (e) => {
      if (e.key === "End") { e.preventDefault(); this.toBottom(); }
    });

    let debounce = 0;
    const search = this.root.querySelector(".log-search input");
    search.addEventListener("input", () => {
      clearTimeout(debounce);
      debounce = setTimeout(() => {
        this.query = search.value.trim().toLowerCase();
        this.applyFilter();
      }, 150);
    });

    for (const b of this.root.querySelectorAll(".log-levels button")) {
      b.setAttribute("aria-pressed", String(b.classList.contains("active")));
      b.addEventListener("click", () => {
        for (const o of this.root.querySelectorAll(".log-levels button")) {
          o.classList.toggle("active", o === b);
          o.setAttribute("aria-pressed", String(o === b));
        }
        this.minLevel = MIN_LEVEL[b.dataset.level] ?? INFO;
        this.applyFilter();
      });
    }

    if (this.replicas.length > 1) this.addReplicaSelect();

    // Tocar una línea la muestra entera, con la hora completa.
    this.win.addEventListener("click", (e) => {
      const row = e.target.closest(".log-line");
      if (row && row.line && !window.getSelection().toString()) this.showDetail(row.line);
    });
    this.dialog.querySelector("[data-close]").addEventListener("click", () => this.dialog.close());
    this.dialog.addEventListener("click", (e) => {
      if (e.target === this.dialog) this.dialog.close(); // clic en el fondo
    });
    this.dialog.querySelector("[data-copy]").addEventListener("click", async (e) => {
      const btn = e.currentTarget;
      try {
        await navigator.clipboard.writeText(this.dialog.querySelector(".log-detail-text").textContent);
        btn.querySelector("i").textContent = "check";
        setTimeout(() => { btn.querySelector("i").textContent = "content_copy"; }, 1500);
      } catch {
        // Sin HTTPS no hay portapapeles: queda seleccionar a mano.
      }
    });

    window.addEventListener("resize", () => { this.fitHeight(); this.schedule(); });
    // El navegador del celular corta las conexiones de una pestaña en
    // segundo plano; al volver, si el stream murió, se reabre.
    document.addEventListener("visibilitychange", () => {
      if (document.visibilityState === "visible" && this.es && this.es.readyState === EventSource.CLOSED && !this.ended) {
        this.connect();
      }
    });
  }

  addReplicaSelect() {
    const field = el("div", "field small suffix round border no-margin log-replicas");
    const select = el("select");
    select.setAttribute("aria-label", "Réplica");
    select.append(el("option", "", "Todas las réplicas"));
    select.options[0].value = "";
    for (const name of this.replicas) {
      const o = el("option", "", name);
      o.value = name;
      select.append(o);
    }
    field.append(select, el("i", "", "arrow_drop_down"));
    this.statusEl.before(field);
    select.addEventListener("change", () => {
      this.container = select.value;
      this.connect();
    });
  }

  // fitHeight hace que el visor llene la pantalla hasta abajo, descontando
  // la barra de navegación del celular si se ve.
  fitHeight() {
    if (!this.opts.fit) return;
    const top = this.scroll.getBoundingClientRect().top + window.scrollY;
    const nav = document.querySelector("nav.bottom");
    const navH = nav && nav.offsetParent !== null ? nav.getBoundingClientRect().height : 0;
    const h = window.innerHeight - top - navH - 16;
    this.scroll.style.height = Math.max(240, h) + "px";
  }

  connect() {
    if (this.es) this.es.close();
    this.buf.clear();
    this.pending = [];
    this.unseen = 0;
    this.follow = true;
    this.ended = false;
    this.render();

    const url = new URL(this.streamURL, location.href);
    if (this.container) url.searchParams.set("container", this.container);
    const es = new EventSource(url);
    this.es = es;
    this.setStatus("unknown", "Conectando…");

    es.onopen = () => this.setStatus("up", "En vivo");
    es.onmessage = (e) => this.enqueue(JSON.parse(e.data));
    es.addEventListener("notice", (e) => this.enqueueNotice(JSON.parse(e.data), WARN));
    es.addEventListener("state", (e) => this.enqueueState(JSON.parse(e.data)));
    // El contenedor se detuvo pero la conexión sigue: llegan los cambios de
    // estado y, si arranca de nuevo, "started" y las líneas nuevas.
    es.addEventListener("stopped", (e) => {
      this.enqueueNotice(JSON.parse(e.data), INFO);
      this.setStatus("down", "Detenido");
    });
    es.addEventListener("started", (e) => {
      this.enqueueNotice(JSON.parse(e.data), INFO);
      this.setStatus("up", "En vivo");
    });
    es.addEventListener("end", (e) => {
      // Sin cerrar, el navegador reconectaría solo a los 3 s.
      es.close();
      this.ended = true;
      const data = JSON.parse(e.data);
      if (this.opts.onEnd) {
        const [dot, text] = this.opts.onEnd(data);
        this.enqueueNotice(data.text, data.ok ? INFO : ERROR);
        this.setStatus(dot, text);
        return;
      }
      this.enqueueNotice(data, INFO);
      this.setStatus("down", "Detenido", () => this.connect());
    });
    es.onerror = () => {
      if (this.ended) return;
      if (es.readyState === EventSource.CONNECTING) {
        // Reconecta solo, con Last-Event-ID: no se repite nada.
        this.setStatus("unknown", "Reconectando…");
      } else {
        // Cerrado del todo: la sesión venció (el login devuelve HTML) o el
        // servicio ya no existe.
        this.setStatus("down", "Desconectado", () => location.reload());
        if (this.opts.onEnd) this.opts.onEnd(null);
      }
    };
  }

  // setStatus actualiza el chip de estado; con onClick, el chip ofrece
  // reintentar.
  setStatus(dot, text, onClick) {
    const s = this.statusEl;
    s.replaceChildren(el("span", "dot " + dot), el("span", "", onClick ? text + " · reintentar" : text));
    s.onclick = onClick || null;
    s.classList.toggle("clickable", !!onClick);
  }

  enqueue(ev) {
    const text = stripAnsi(ev.line);
    const ts = ev.ts && !ev.ts.startsWith("0001-") ? new Date(ev.ts) : null;
    this.pending.push({
      ts,
      time: ts ? timeFmt.format(ts) : "",
      stream: ev.stream,
      c: ev.c || "",
      text,
      lower: null, // en minúsculas, se calcula al buscar por primera vez
      level: detectLevel(text),
    });
    if (this.pending.length > MAX_PENDING) this.pending.splice(0, this.pending.length - MAX_PENDING);
    this.schedule();
  }

  enqueueNotice(text, level) {
    const ts = new Date();
    this.pending.push({ ts, time: timeFmt.format(ts), stream: "maestro", c: "", text, lower: null, level, notice: true });
    this.schedule();
  }

  // enqueueState agrega un cambio de estado del servicio. Pasa el filtro de
  // nivel como los avisos (es parte de la línea de tiempo, no del log), y
  // su nivel es el del estado: caído es error, degradado es aviso.
  enqueueState(ev) {
    const ts = new Date(ev.ts);
    const level = ev.state === "down" ? ERROR : ev.state === "degraded" ? WARN : INFO;
    const text = ev.reason ? `${ev.label}: ${ev.reason}` : ev.label;
    this.pending.push({ ts, time: timeFmt.format(ts), stream: "estado", c: "", text, lower: null, level, notice: true, state: ev.state });
    this.schedule();
  }

  // schedule junta todo lo que llega en un cuadro y dibuja una sola vez.
  schedule() {
    if (!this.frame) this.frame = requestAnimationFrame(() => { this.frame = 0; this.flush(); });
  }

  flush() {
    this.buf.cap = this.follow ? CAP : PAUSED_CAP;
    if (this.pending.length) {
      const before = this.buf.size;
      const dropped = this.buf.push(this.pending);
      const added = this.buf.size - before + dropped;
      this.pending = [];
      if (!this.follow) {
        this.unseen += added;
        // Las líneas descartadas arriba corren todo hacia arriba: se
        // compensa para que lo que se está leyendo quede quieto.
        if (dropped) this.scroll.scrollTop -= dropped * this.lineH;
      }
    }
    this.render();
  }

  matches(l) {
    if (l.level < this.minLevel && !l.notice) return false;
    if (!this.query) return true;
    if (l.lower === null) l.lower = (l.c + " " + l.text).toLowerCase();
    return l.lower.includes(this.query);
  }

  applyFilter() {
    const on = this.query || this.minLevel > INFO;
    this.buf.setFilter(on ? (l) => this.matches(l) : null);
    this.follow = true;
    this.unseen = 0;
    this.render();
  }

  atBottom() {
    const s = this.scroll;
    return s.scrollTop + s.clientHeight >= s.scrollHeight - this.lineH;
  }

  toBottom() {
    this.follow = true;
    this.unseen = 0;
    this.render();
  }

  render() {
    const n = this.buf.size;
    this.spacer.style.height = n * this.lineH + "px";
    if (this.follow) this.scroll.scrollTop = this.scroll.scrollHeight;

    const first = Math.max(0, Math.floor(this.scroll.scrollTop / this.lineH) - OVERSCAN);
    const count = Math.min(n - first, Math.ceil(this.scroll.clientHeight / this.lineH) + 2 * OVERSCAN);
    this.win.style.transform = `translateY(${first * this.lineH}px)`;

    while (this.rows.length < count) {
      const row = el("div", "log-line");
      this.rows.push(row);
      this.win.append(row);
    }
    for (let i = 0; i < this.rows.length; i++) {
      const row = this.rows[i];
      if (i >= count) {
        row.hidden = true;
        row.line = null;
        continue;
      }
      row.hidden = false;
      const l = this.buf.at(first + i);
      // Se rearma solo si cambió la línea o la búsqueda.
      if (row.line === l && row.query === this.query) continue;
      row.line = l;
      row.query = this.query;
      this.fillRow(row, l);
    }

    this.bottomBtn.hidden = this.follow;
    const label = this.bottomBtn.querySelector("span");
    label.textContent = this.unseen ? `${this.unseen > 999 ? "999+" : this.unseen} nuevas` : "Ir al final";

    if (n === 0) {
      this.win.dataset.empty = this.buf.lines.length ? "Nada coincide con el filtro." : "Sin líneas todavía.";
    } else {
      delete this.win.dataset.empty;
    }
  }

  fillRow(row, l) {
    row.className =
      "log-line" +
      (l.state ? " state state-" + l.state : l.notice ? " notice" : "") +
      (l.level ? " " + LEVEL_CLASS[l.level] : "");
    const parts = [el("span", "log-time", l.time)];
    if (l.state) parts.push(el("span", "dot " + l.state));
    if (l.c) parts.push(el("span", "log-container", l.c));
    parts.push(this.highlight(l.text));
    row.replaceChildren(...parts);
  }

  // highlight marca las apariciones de la búsqueda con <mark>. Todo va como
  // texto (textContent), nunca como HTML: una línea de log no puede meter
  // marcado en la página.
  highlight(text) {
    const span = el("span", "log-text");
    if (!this.query) {
      span.textContent = text;
      return span;
    }
    const lower = text.toLowerCase();
    let at = 0;
    for (let i = lower.indexOf(this.query); i >= 0; i = lower.indexOf(this.query, at)) {
      if (i > at) span.append(text.slice(at, i));
      span.append(el("mark", "", text.slice(i, i + this.query.length)));
      at = i + this.query.length;
    }
    if (at < text.length) span.append(text.slice(at));
    return span;
  }

  showDetail(l) {
    const meta = [l.ts ? fullFmt.format(l.ts) : "sin hora", l.stream];
    if (l.c) meta.push(l.c);
    this.dialog.querySelector(".log-detail-meta").textContent = meta.join(" · ");
    this.dialog.querySelector(".log-detail-text").textContent = pretty(l.text);
    this.dialog.showModal();
  }
}

// pretty indenta una línea que es un objeto JSON (caddy, pino, slog en
// JSON); cualquier otra cosa queda igual.
function pretty(text) {
  const t = text.trim();
  if (!t.startsWith("{")) return text;
  try {
    return JSON.stringify(JSON.parse(t), null, 2);
  } catch {
    return text;
  }
}

