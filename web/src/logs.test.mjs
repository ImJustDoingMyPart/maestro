// Tests de la lógica pura del visor. No los corre go test: necesitan Node
// (node --test web/src/), que el proyecto no exige para compilar.
import { test } from "node:test";
import assert from "node:assert/strict";
import { detectLevel, stripAnsi, INFO, WARN, ERROR } from "./level.js";
import { LogBuffer } from "./buffer.js";

test("detectLevel", () => {
  const cases = [
    ["ERROR: relation does not exist", ERROR],
    ["2026/11/10 FATAL boom", ERROR],
    ['time=x level=ERROR msg="falló"', ERROR],
    ["t=x lvl=eror msg=x", ERROR],
    ['{"level":"error","msg":"x"}', ERROR],
    ['{"severity": "ERROR"}', ERROR],
    ["[error] 12#12: connect() failed", ERROR],
    ['{"level":50,"msg":"x"}', ERROR],
    ["WARN deprecated", WARN],
    ['{"level":"warn"}', WARN],
    ["level=warning msg=x", WARN],
    ['{"level":40}', WARN],
    ["0 errors, 2 warnings", INFO],
    ["error handling refactor", INFO],
    ['{"level":"info","msg":"error count 0"}', INFO],
    ['{"level":30}', INFO],
    ["ERRORS=0", INFO],
    ["", INFO],
  ];
  for (const [s, want] of cases) assert.equal(detectLevel(s), want, s);
});

test("stripAnsi", () => {
  assert.equal(stripAnsi("\x1b[32mINFO\x1b[0m listo"), "INFO listo");
  assert.equal(stripAnsi("\x1b[?25lnada"), "nada");
  assert.equal(stripAnsi("sin escapes"), "sin escapes");
});

const mk = (n) => Array.from({ length: n }, (_, i) => ({ text: `l${i}`, even: i % 2 === 0 }));

test("buffer sin filtro descarta las viejas de a tandas", () => {
  const b = new LogBuffer(100);
  assert.equal(b.push(mk(112)), 0); // dentro del margen de 1/8
  assert.equal(b.size, 112);
  const dropped = b.push(mk(1));
  assert.equal(dropped, 13);
  assert.equal(b.size, 100);
  assert.equal(b.at(0).seq, 13);
  assert.equal(b.at(99).seq, 112);
});

test("buffer con filtro", () => {
  const b = new LogBuffer(100);
  b.push(mk(10));
  b.setFilter((l) => l.even);
  assert.equal(b.size, 5);
  assert.equal(b.at(1).text, "l2");
  b.push(mk(2)); // l0 (par) y l1
  assert.equal(b.size, 6);
  assert.equal(b.at(5).seq, 10);

  // Al descartar, la vista pierde solo las filas filtradas que se fueron.
  const dropped = b.push(mk(101));
  assert.equal(b.lines.length, 100);
  assert.equal(b.at(0).seq >= b.base, true);
  assert.equal(dropped, 7); // las pares de las 13 descartadas: seq 0,2,…,12
  b.setFilter(null);
  assert.equal(b.size, 100);
});
