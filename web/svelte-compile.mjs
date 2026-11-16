// Compila un componente de Svelte a JavaScript: lo llama
// internal/sveltebuild, una vez por archivo .svelte, mientras esbuild arma
// el bundle. Recibe la ruta del archivo y escribe el JavaScript en la
// salida estándar. Los componentes no llevan <style>: usan las clases de
// maestro.css, como las plantillas.
import { readFileSync } from "node:fs";
import { compile } from "svelte/compiler";

const file = process.argv[2];
const out = compile(readFileSync(file, "utf8"), {
  filename: file,
  generate: "client",
  css: "injected",
  dev: false,
});
for (const w of out.warnings) {
  process.stderr.write(`${w.filename}:${w.start?.line ?? "?"}: ${w.message}\n`);
}
process.stdout.write(out.js.code);
