// Tests de la lectura de los mensajes de docker compose config. Los
// mensajes son los que da Compose 5.7.0 (probados a mano). Se corren con
// node --test web/src/.
import { test } from "node:test";
import assert from "node:assert/strict";
import { issuesFor, tabsToSpaces, pathSpot } from "./issues.js";

const P = "/home/x/docker/demo/compose.yml";
// lines arma los renglones como los usa issues.js (texto y posición).
const lines = (t) => {
  let from = 0;
  return t.split("\n").map((text) => { const l = { text, from, to: from + text.length }; from += text.length + 1; return l; });
};
const at = (text, i) => text.slice(i.from, i.to);

test("tabulación: la marca la revisión local, una sola vez", () => {
  const text = "services:\n\tweb:\n\t\timage: nginx\n";
  const got = issuesFor({
    text, file: "compose", base: "compose.yml",
    error: `failed to parse ${P}: go-yaml load error in scanner (while scanning for the next token) at L2.C1: found character that cannot start any token`,
  });
  assert.deepEqual(got.map((i) => i.line), [2, 3]);
  assert.ok(got.every((i) => i.severity === "error" && /tabulación/.test(i.message)));
});

test("sangría: el final del tramo", () => {
  const text = 'services:\n  web:\n    image: nginx\n   ports:\n      - "80:80"\n';
  const [i] = issuesFor({ text, file: "compose", base: "compose.yml", error: `failed to parse ${P}: go-yaml load error in parser (while parsing a block mapping) at L2.C3-L4.C4: did not find expected key` });
  assert.equal(i.line, 4);
  assert.equal(at(text, i), "ports:");
  assert.match(i.message, /sangría/);
});

test("clave repetida", () => {
  const text = "services:\n  web:\n    image: nginx\n    image: caddy\n";
  const [i] = issuesFor({ text, file: "compose", base: "compose.yml", error: `failed to parse ${P}: yaml: construct errors: line 1: line 4: mapping key "image" already defined at line 3` });
  assert.equal(i.line, 4);
  assert.equal(i.message, "«image» está repetida (también en la línea 3).");
});

test("clave mal escrita, por ruta", () => {
  const text = "services:\n  web:\n    image: nginx\n    restar: always\n  db:\n    restar: x\n";
  const [i] = issuesFor({ text, file: "compose", base: "compose.yml", error: `validating ${P}: services.web.restar false schema` });
  assert.equal(i.line, 4);
  assert.match(i.message, /«restar» no es una opción/);
  const [j] = issuesFor({ text: "services:\n  web:\n    healthcheck:\n      tes: x\n", file: "compose", base: "compose.yml", error: `validating ${P}: services.web.healthcheck additional properties 'tes' not allowed` });
  assert.equal(j.line, 4);
});

test("variable sin definir: cada uso, salvo $$", () => {
  const text = "services:\n  web:\n    image: nginx:${TAG}\n    environment:\n      D: ${TAG:-x}\n      A: $TAG\n      B: $$TAG\n      C: $TAGS\n";
  const got = issuesFor({ text, file: "compose", base: "compose.yml", warnings: [`The "TAG" variable is not set. Defaulting to a blank string.`] });
  assert.deepEqual(got.map((i) => [i.line, at(text, i), i.severity]), [[3, "${TAG}", "warning"], [6, "$TAG", "warning"]]);
  assert.match(got[0].message, /escribilo \$\$/);
});

test("la contraseña con $ adentro", () => {
  const text = "services:\n  web:\n    environment:\n      PASS: abc$def\n";
  const [i] = issuesFor({ text, file: "compose", base: "compose.yml", warnings: [`The "def" variable is not set. Defaulting to a blank string.`] });
  assert.equal(at(text, i), "$def");
});

test("version obsoleta", () => {
  const text = 'version: "3"\nservices: {}\n';
  const [i] = issuesFor({ text, file: "compose", base: "compose.yml", warnings: [`${P}: the attribute \`version\` is obsolete, it will be ignored, please remove it to avoid potential confusion`] });
  assert.equal(i.line, 1);
  assert.match(i.raw, /^compose\.yml: the attribute/);
});

test("servicio sin image y lista con índice", () => {
  const text = "services:\n  web:\n    ports:\n      - 80:80\n      - foo: 1\n";
  const [i] = issuesFor({ text, file: "compose", base: "compose.yml", error: 'service "web" has neither an image nor a build context specified: invalid compose project' });
  assert.equal(i.line, 2);
  const [j] = issuesFor({ text, file: "compose", base: "compose.yml", error: "service ports services.web.ports.[1] is missing a target port" });
  assert.equal(j.line, 3);
});

test("un error del .env no se ubica en el compose, sí en el .env", () => {
  const raw = "failed to read /home/x/docker/demo/.env: line 2: key cannot contain a space";
  const [c] = issuesFor({ text: "services: {}\n", file: "compose", base: "compose.yml", error: raw });
  assert.equal(c.line, undefined);
  const [e] = issuesFor({ text: "A=1\nB C=2\n", file: "env", base: ".env", error: raw });
  assert.equal(e.line, 2);
  assert.match(e.message, /espacios/);
});

test("mensaje desconocido: el original, sin ubicar", () => {
  const [i] = issuesFor({ text: "x", file: "compose", base: "compose.yml", error: "algo raro\nsegunda línea" });
  assert.equal(i.message, "algo raro");
  assert.equal(i.line, undefined);
});

test("tabsToSpaces solo toca la sangría", () => {
  assert.equal(tabsToSpaces("a:\n\tb: 1\n \t\tc: \"x\ty\"\n"), "a:\n  b: 1\n     c: \"x\ty\"\n");
});

test("pathSpot no confunde servicios", () => {
  const l = lines("services:\n  db:\n    image: x\n  web:\n    image: y\n");
  assert.equal(pathSpot(l, ["services", "web", "image"])[0].line, 5);
  assert.equal(pathSpot(l, ["services", "web", "nada"])[0].line, 4);
  assert.deepEqual(pathSpot(l, ["networks", "x"]), []);
});
