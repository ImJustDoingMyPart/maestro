// Pestaña Compose: editor del compose.yml y del .env, guardar con diff y
// confirmación, y los botones up/down/restart/pull con la salida en vivo.
//
// El contenido llega en <textarea> dentro de la página (html/template lo
// escapa); el editor se arma encima y los textarea quedan como fuente.

import { EditorState, Compartment } from "@codemirror/state";
import {
  EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter,
  drawSelection, highlightSpecialChars,
} from "@codemirror/view";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import {
  indentOnInput, bracketMatching, foldGutter, foldKeymap, syntaxHighlighting,
  HighlightStyle, StreamLanguage, indentUnit,
} from "@codemirror/language";
import { searchKeymap, highlightSelectionMatches } from "@codemirror/search";
import { setDiagnostics, lintGutter } from "@codemirror/lint";
import { yaml } from "@codemirror/lang-yaml";
import { tags as t } from "@lezer/highlight";

import { Viewer } from "./viewer.js";
import { dotenv } from "./dotenv.js";
import { issuesFor, tabsToSpaces } from "./issues.js";

// checkDelay es la pausa sin teclear después de la cual se revisa el
// archivo con docker compose config.
const checkDelay = 1000;

// Colores de la paleta de Material 3 de Beer CSS: siguen al tema claro u
// oscuro y al color semilla sin tocar nada acá.
const theme = EditorView.theme({
  "&": {
    color: "var(--on-surface)",
    backgroundColor: "var(--surface-container-lowest)",
    border: "1px solid var(--outline-variant)",
    borderRadius: "0.5rem",
    fontSize: "0.875rem",
  },
  "&.cm-focused": { outline: "2px solid var(--primary)", outlineOffset: "-1px" },
  ".cm-scroller": { fontFamily: "ui-monospace, 'JetBrains Mono', 'Cascadia Code', Menlo, Consolas, monospace", lineHeight: "1.5" },
  ".cm-content": { caretColor: "var(--primary)" },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--primary)" },
  ".cm-gutters": {
    backgroundColor: "var(--surface-container-low)",
    color: "var(--on-surface-variant)",
    border: "none",
    borderRadius: "0.5rem 0 0 0.5rem",
  },
  ".cm-activeLine": { backgroundColor: "color-mix(in srgb, var(--primary) 6%, transparent)" },
  ".cm-activeLineGutter": { backgroundColor: "color-mix(in srgb, var(--primary) 12%, transparent)", color: "var(--on-surface)" },
  "&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection": {
    backgroundColor: "color-mix(in srgb, var(--primary) 28%, transparent)",
  },
  ".cm-selectionMatch": { backgroundColor: "color-mix(in srgb, var(--tertiary) 20%, transparent)" },
  ".cm-searchMatch": { backgroundColor: "color-mix(in srgb, var(--tertiary) 30%, transparent)", outline: "1px solid var(--tertiary)" },
  ".cm-searchMatch.cm-searchMatch-selected": { backgroundColor: "color-mix(in srgb, var(--tertiary) 55%, transparent)" },
  ".cm-matchingBracket": { backgroundColor: "color-mix(in srgb, var(--secondary) 30%, transparent)", outline: "none" },
  ".cm-foldPlaceholder": { backgroundColor: "var(--surface-container-high)", color: "var(--on-surface-variant)", border: "none" },
  ".cm-panels": { backgroundColor: "var(--surface-container)", color: "var(--on-surface)" },
  ".cm-panels.cm-panels-bottom": { borderTop: "1px solid var(--outline-variant)" },
  ".cm-panel.cm-search": { padding: "0.5rem" },
  ".cm-panel.cm-search input, .cm-panel.cm-search button": { fontSize: "0.875rem" },
  ".cm-textfield": {
    backgroundColor: "var(--surface-container-lowest)", color: "var(--on-surface)",
    border: "1px solid var(--outline)", borderRadius: "0.25rem",
  },
  ".cm-button": {
    backgroundImage: "none", backgroundColor: "var(--secondary-container)",
    color: "var(--on-secondary-container)", border: "none", borderRadius: "1rem", padding: "0.125rem 0.75rem",
  },
  // Problemas: subrayado ondulado y un punto en el margen, con los colores
  // de estado (los de CodeMirror vienen fijos en un SVG).
  ".cm-lintRange": { backgroundImage: "none", paddingBottom: "0", textDecoration: "underline wavy", textDecorationSkipInk: "none", textUnderlineOffset: "0.25rem" },
  ".cm-lintRange-error": { textDecorationColor: "var(--error)" },
  ".cm-lintRange-warning": { textDecorationColor: "var(--status-warn)" },
  ".cm-lintRange-active": { backgroundColor: "color-mix(in srgb, var(--primary) 12%, transparent)" },
  ".cm-gutter-lint": { width: "1rem" },
  ".cm-gutter-lint .cm-gutterElement": { display: "flex", alignItems: "center", justifyContent: "center", padding: "0" },
  ".cm-lint-marker": { content: "normal", width: "0.5rem", height: "0.5rem", borderRadius: "50%" },
  ".cm-lint-marker-error": { backgroundColor: "var(--error)" },
  ".cm-lint-marker-warning": { backgroundColor: "var(--status-warn)" },
  ".cm-tooltip": { backgroundColor: "var(--surface-container-high)", color: "var(--on-surface)", border: "none", borderRadius: "0.5rem" },
  ".cm-tooltip-lint": { padding: "0.25rem 0", maxWidth: "min(32rem, 90vw)", fontFamily: "var(--font)" },
  ".cm-diagnostic": { padding: "0.25rem 0.75rem", borderLeftWidth: "0.25rem" },
  ".cm-diagnostic-error": { borderLeftColor: "var(--error)" },
  ".cm-diagnostic-warning": { borderLeftColor: "var(--status-warn)" },
});

const highlight = HighlightStyle.define([
  { tag: [t.propertyName, t.definition(t.propertyName)], color: "var(--primary)" },
  { tag: [t.string, t.special(t.string)], color: "var(--tertiary)" },
  { tag: [t.number, t.bool, t.null, t.atom], color: "var(--secondary)", fontWeight: "500" },
  { tag: [t.comment, t.lineComment], color: "var(--on-surface-variant)", fontStyle: "italic" },
  { tag: [t.labelName, t.meta, t.typeName, t.annotation, t.special(t.variableName)], color: "var(--secondary)" },
  { tag: [t.punctuation, t.separator, t.squareBracket, t.brace, t.operator], color: "var(--outline)" },
  { tag: t.keyword, color: "var(--primary)", fontWeight: "500" },
  { tag: t.invalid, color: "var(--error)" },
]);

const editable = new Compartment(); // se apaga mientras se guarda

function extensions(file, onChange) {
  return [
    lintGutter(),
    lineNumbers(),
    highlightActiveLineGutter(),
    highlightSpecialChars(),
    history(),
    foldGutter(),
    drawSelection(),
    indentOnInput(),
    bracketMatching(),
    highlightActiveLine(),
    highlightSelectionMatches(),
    indentUnit.of("  "),
    EditorState.tabSize.of(2),
    keymap.of([...defaultKeymap, ...searchKeymap, ...historyKeymap, ...foldKeymap, indentWithTab]),
    file === "env" ? dotenv() : yaml(),
    syntaxHighlighting(highlight),
    theme,
    editable.of(EditorView.editable.of(true)),
    EditorView.updateListener.of((u) => { if (u.docChanged) onChange(); }),
    // YAML no acepta tabulaciones en la sangría: lo pegado llega con
    // espacios. (La tecla Tab ya indenta con espacios: indentUnit.)
    file === "env" ? [] : EditorView.clipboardInputFilter.of((text) => tabsToSpaces(text)),
    EditorView.contentAttributes.of({ "aria-label": file === "env" ? ".env" : "compose.yml" }),
  ];
}

function plural(n, one, many) {
  return `${n} ${n === 1 ? one : many}`;
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// post manda JSON y devuelve [status, cuerpo]. Si la sesión venció, el hub
// redirige al login y la respuesta es HTML: eso se convierte en un error
// legible.
async function post(url, body) {
  let res;
  try {
    res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: "same-origin",
    });
  } catch {
    return [0, { error: "No se pudo hablar con el hub. ¿Hay conexión?" }];
  }
  const type = res.headers.get("Content-Type") || "";
  if (!type.includes("application/json")) {
    return [res.status, { error: "La sesión venció o el hub contestó otra cosa. Recargá la página." }];
  }
  return [res.status, await res.json()];
}

class Compose {
  constructor(root) {
    this.root = root;
    this.base = root.dataset.base;
    this.msg = root.querySelector(".compose-msg");
    this.saveBtn = root.querySelector("[data-save]");
    this.revertBtn = root.querySelector("[data-revert]");
    this.pathEl = root.querySelector(".editor-path");
    this.diffDialog = document.querySelector(".diff-dialog");
    this.downDialog = document.querySelector(".down-dialog");
    this.actionBtns = [...root.querySelectorAll("[data-action]")];
    this.busyNote = root.querySelector(".compose-busy");
    this.checkEl = root.querySelector(".editor-check");
    this.issuesEl = root.querySelector(".editor-issues");

    // Un EditorState por archivo; la vista es una sola y se le cambia el
    // estado al cambiar de pestaña (así cada archivo guarda su historial
    // de deshacer).
    this.files = {};
    for (const ta of root.querySelectorAll("textarea.editor-source")) {
      const name = ta.dataset.file;
      // seq cuenta las ediciones, para descartar la respuesta de una
      // revisión de un texto que ya cambió; checked es el texto revisado e
      // issues lo que se encontró (null mientras no hay respuesta).
      const f = {
        name, path: ta.dataset.path, hash: ta.dataset.hash, saved: ta.value, state: null,
        seq: 0, timer: 0, checked: null, issues: null, checkFail: "",
      };
      f.state = EditorState.create({
        doc: ta.value,
        extensions: extensions(name, () => { this.updateDirty(); this.scheduleCheck(name); }),
      });
      this.files[name] = f;
      ta.hidden = true;
    }
    this.current = "compose";
    this.view = new EditorView({ state: this.files.compose.state, parent: root.querySelector(".editor-host") });

    this.output = root.querySelector(".run-output");
    this.viewer = new Viewer(this.output, {
      autoconnect: false,
      fit: false,
      onEnd: (data) => this.actionEnded(data),
    });

    this.bind();
    this.showFile("compose");
    if (root.dataset.run) this.watchRun(root.dataset.run, root.dataset.runName);
    else if (root.hasAttribute("data-busy")) this.setBusy(true, true);
  }

  bind() {
    for (const tab of this.root.querySelectorAll(".file-tabs [data-file]")) {
      tab.addEventListener("click", () => this.showFile(tab.dataset.file));
    }
    this.saveBtn.addEventListener("click", () => this.save());
    this.revertBtn.addEventListener("click", () => this.revert());
    this.msg.querySelector("[data-dismiss]").addEventListener("click", () => { this.msg.hidden = true; });

    // Ctrl+S / Cmd+S guarda en vez de bajar la página.
    document.addEventListener("keydown", (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key === "s") {
        e.preventDefault();
        if (!this.saveBtn.disabled) this.save();
      }
    });
    window.addEventListener("beforeunload", (e) => {
      if (this.dirtyFiles().length) e.preventDefault();
    });

    for (const b of this.actionBtns) {
      b.addEventListener("click", () => {
        if (b.dataset.action === "down") this.confirmDown();
        else this.runAction(b.dataset.action);
      });
    }
    for (const d of [this.diffDialog, this.downDialog]) {
      d.querySelector("[data-cancel]").addEventListener("click", () => d.close());
    }
  }

  // Archivos

  text(name) {
    return name === this.current ? this.view.state.doc.toString() : this.files[name].state.doc.toString();
  }

  dirtyFiles() {
    return Object.keys(this.files).filter((n) => this.text(n) !== this.files[n].saved);
  }

  showFile(name) {
    if (name !== this.current) {
      this.files[this.current].state = this.view.state;
      this.view.setState(this.files[name].state);
      this.current = name;
    }
    for (const tab of this.root.querySelectorAll(".file-tabs [data-file]")) {
      const on = tab.dataset.file === name;
      tab.classList.toggle("active", on);
      tab.setAttribute("aria-selected", String(on));
    }
    const f = this.files[name];
    this.pathEl.textContent = f.path + (f.hash ? "" : " (no existe: se crea al guardar)");
    this.updateDirty();
    if (!f.timer && f.checked !== this.text(name)) this.check(name);
    this.renderIssues();
  }

  // Revisión mientras se edita: después de una pausa, el mismo check que
  // usa Guardar (docker compose config en el agent, sin escribir nada).

  scheduleCheck(name) {
    const f = this.files[name];
    f.seq++;
    clearTimeout(f.timer);
    f.timer = setTimeout(() => { f.timer = 0; this.check(name); }, checkDelay);
    if (name === this.current) this.renderIssues();
  }

  async check(name) {
    const f = this.files[name];
    const seq = f.seq;
    const content = this.text(name);
    const [status, res] = await post(`${this.base}/files/${name}/check`, { content });
    if (seq !== f.seq) return; // se siguió editando: ya viene otra revisión
    this.applyCheck(name, content, status, res);
  }

  // applyCheck guarda el resultado de un check del texto content y lo
  // marca en el editor.
  applyCheck(name, content, status, res) {
    const f = this.files[name];
    f.checked = content;
    f.checkFail = "";
    if (status === 200 || status === 422) {
      f.issues = issuesFor({
        text: content, file: name, base: f.path.split("/").pop(),
        error: status === 422 ? res.output || res.error : "",
        warnings: status === 200 ? res.warnings || [] : [],
      });
    } else {
      f.issues = null;
      f.checkFail = res.error || `el hub contestó ${status}`;
    }
    const diags = (f.issues || []).filter((i) => i.from !== undefined).map((i) => ({
      from: i.from, to: i.to, severity: i.severity, message: i.message,
    }));
    if (name === this.current) this.view.dispatch(setDiagnostics(this.view.state, diags));
    else f.state = f.state.update(setDiagnostics(f.state, diags)).state;
    if (name === this.current) this.renderIssues();
  }

  // renderIssues muestra, debajo del editor, cómo está el archivo actual y
  // la lista de problemas (en el celular no hay tooltips: la lista es la
  // forma de leerlos).
  renderIssues() {
    const f = this.files[this.current];
    const c = this.checkEl;
    const issues = f.issues || [];
    const errors = issues.filter((i) => i.severity === "error").length;
    const warns = issues.length - errors;
    let text, cls = "secondary-text";
    if (f.timer || f.checked !== this.text(this.current)) text = "Revisando…";
    else if (f.checkFail) { text = `No se pudo revisar: ${f.checkFail}`; cls = "reason degraded"; }
    else if (!issues.length) text = "Sin problemas";
    else {
      text = [errors && plural(errors, "error", "errores"), warns && plural(warns, "aviso", "avisos")].filter(Boolean).join(" · ");
      cls = "reason " + (errors ? "down" : "degraded");
    }
    c.className = "small-text editor-check " + cls;
    c.textContent = text;
    this.fillIssues(this.issuesEl, issues, true);
  }

  // fillIssues llena una caja de problemas (la de debajo del editor o la
  // del diálogo de guardar). Con jump, "Línea N" lleva a la línea.
  fillIssues(box, issues, jump) {
    box.hidden = !issues.length;
    const errors = issues.some((i) => i.severity === "error");
    box.classList.toggle("down", errors);
    box.classList.toggle("degraded", !errors);
    box.querySelector(".issues-icon").textContent = errors ? "error" : "warning";
    const list = box.querySelector(".issues-list");
    list.replaceChildren();
    for (const i of issues) {
      const row = el("div", "issue");
      if (i.line) {
        let at;
        if (jump) {
          at = el("a", "issue-line", `Línea ${i.line}`);
          at.href = "#";
          at.addEventListener("click", (e) => { e.preventDefault(); this.jumpTo(i.line); });
        } else at = el("span", "issue-line", `Línea ${i.line}`);
        row.append(at, " ");
      }
      row.append(i.message);
      // En el diálogo de guardar, solo la explicación: el original está
      // en la lista de debajo del editor.
      if (jump && i.raw && i.raw !== i.message) row.append(el("div", "issue-raw", i.raw));
      list.append(row);
    }
  }

  jumpTo(line) {
    const doc = this.view.state.doc;
    const l = doc.line(Math.min(line, doc.lines));
    this.view.dispatch({ selection: { anchor: l.from }, scrollIntoView: true });
    this.view.focus();
  }

  updateDirty() {
    const dirty = this.dirtyFiles();
    for (const tab of this.root.querySelectorAll(".file-tabs [data-file]")) {
      tab.classList.toggle("dirty", dirty.includes(tab.dataset.file));
    }
    const cur = dirty.includes(this.current);
    this.saveBtn.disabled = !cur || this.saving;
    this.revertBtn.disabled = !cur || this.saving;
  }

  revert() {
    const f = this.files[this.current];
    this.view.dispatch({ changes: { from: 0, to: this.view.state.doc.length, insert: f.saved } });
  }

  setSaving(on) {
    this.saving = on;
    this.view.dispatch({ effects: editable.reconfigure(EditorView.editable.of(!on)) });
    this.updateDirty();
  }

  // save valida primero (el agent corre docker compose config y devuelve
  // el diff), muestra el diff para confirmar, y recién ahí escribe.
  async save() {
    const name = this.current;
    const f = this.files[name];
    const content = this.view.state.doc.toString();
    this.setSaving(true);
    this.hideMessage();
    try {
      const [status, res] = await post(`${this.base}/files/${name}/check`, { content });
      if (status === 200 || status === 422) this.applyCheck(name, content, status, res);
      if (status === 422) return this.showMessage("error", "docker compose config rechazó el archivo: no se guardó. Los problemas están marcados en el editor y listados arriba.");
      if (status !== 200) return this.showError(res);
      if (!res.diff) return this.showMessage("info", "Sin cambios respecto de lo que hay en el disco.");
      if (!(await this.confirmDiff(f, res.diff, f.issues || []))) return;

      const [st2, saved] = await post(`${this.base}/files/${name}`, { content, hash: f.hash });
      if (st2 !== 200) return this.showError(saved, st2 === 409);
      f.hash = saved.hash;
      f.saved = content;
      let text = `Se guardó ${f.path.split("/").pop()}.`;
      if (saved.commit) text += ` Commit ${saved.commit}.`;
      if (saved.notice) text += ` ${saved.notice}`;
      text += " Para aplicar el cambio, Up.";
      this.showMessage(saved.notice ? "warn" : "ok", text);
      this.showFile(name);
    } finally {
      this.setSaving(false);
    }
  }

  confirmDiff(f, diff, issues) {
    const d = this.diffDialog;
    d.querySelector(".diff-file").textContent = f.path.split("/").pop();
    // Los avisos no frenan el guardado, pero se ven antes de confirmar.
    this.fillIssues(d.querySelector(".diff-warnings"), issues, false);
    const pre = d.querySelector(".diff");
    pre.replaceChildren();
    for (const line of diff.split("\n")) {
      // Los encabezados de git (diff --git, index) no le dicen nada a quien
      // edita; ---/+++ tampoco: el archivo es uno solo.
      if (/^(diff --git|index |--- |\+\+\+ |new file mode)/.test(line)) continue;
      let cls = "";
      if (line.startsWith("@@")) cls = "diff-hunk";
      else if (line.startsWith("+")) cls = "diff-add";
      else if (line.startsWith("-")) cls = "diff-del";
      else if (line.startsWith("\\")) cls = "diff-meta";
      pre.append(el("span", cls, line + "\n"));
    }
    return new Promise((resolve) => {
      const ok = d.querySelector("[data-confirm]");
      const done = (v) => { ok.onclick = null; d.onclose = null; d.close(); resolve(v); };
      ok.onclick = () => done(true);
      d.onclose = () => done(false);
      d.showModal();
      ok.focus();
    });
  }

  // Mensajes

  hideMessage() { this.msg.hidden = true; }

  showMessage(kind, text, output) {
    const icons = { ok: "check_circle", info: "info", warn: "warning", error: "error" };
    // Con prefijo: "error" a secas es una clase de color de Beer CSS.
    this.msg.className = "small-round no-elevate compose-msg msg-" + kind;
    this.msg.querySelector(".msg-icon").textContent = icons[kind];
    const t = this.msg.querySelector(".msg-text");
    t.replaceChildren(text);
    const pre = this.msg.querySelector(".msg-output");
    pre.textContent = output || "";
    pre.hidden = !output;
    this.msg.hidden = false;
    return t;
  }

  showError(res, conflict) {
    const t = this.showMessage("error", res.error || "Error desconocido.", res.output);
    if (conflict && /recarg/i.test(res.error || "")) {
      const b = el("button", "small border", "Recargar");
      b.addEventListener("click", () => {
        this.files[this.current].saved = this.text(this.current); // sin el aviso de cambios sin guardar
        for (const n of Object.keys(this.files)) this.files[n].saved = this.text(n);
        location.reload();
      });
      t.append(" ", b);
    }
  }

  // Acciones

  confirmDown() {
    const d = this.downDialog;
    const ok = d.querySelector("[data-confirm]");
    ok.onclick = () => { d.close(); this.runAction("down"); };
    d.showModal();
  }

  // setBusy traba los botones mientras corre una acción; el de la acción
  // que se lanzó desde acá queda en espera (el ícono gira, motion.js).
  setBusy(on, external, action) {
    for (const b of this.actionBtns) b.disabled = on;
    this.busyNote.hidden = !external;
    const motion = window.maestroMotion;
    if (!motion) return;
    if (this.busyBtn) motion.busy(this.busyBtn, false);
    this.busyBtn = on ? this.actionBtns.find((b) => b.dataset.action === action) : null;
    if (this.busyBtn) motion.busy(this.busyBtn, true);
  }

  async runAction(action) {
    this.setBusy(true, false, action);
    const [status, res] = await post(`${this.base}/actions/${action}`);
    if (status !== 200) {
      this.setBusy(false);
      this.showError(res);
      return;
    }
    this.watchRun(res.run, action);
  }

  watchRun(url, action) {
    this.setBusy(true, false, action);
    this.output.hidden = false;
    this.output.querySelector(".run-title").textContent = `Salida de ${action}`;
    this.viewer.open(url);
    this.output.scrollIntoView({ behavior: "smooth", block: "nearest" });
  }

  actionEnded(data) {
    this.setBusy(false);
    if (!data) return null; // se perdió la conexión: el visor ya lo dice
    return data.ok ? ["up", "Terminó bien"] : ["down", "Falló"];
  }
}

const root = document.querySelector(".compose");
if (root) new Compose(root);
