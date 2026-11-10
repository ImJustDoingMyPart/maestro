// Buffer de las últimas N líneas y la vista filtrada sobre él.
//
// Cada línea lleva un número de secuencia que no se repite. El buffer
// guarda las líneas en un array y, cuando se pasa de la capacidad, descarta
// las más viejas de a tandas (splice de a una sería O(n) por línea). La
// vista filtrada es la lista de secuencias que pasan el filtro.

export class LogBuffer {
  constructor(cap) {
    this.cap = cap;
    this.lines = [];
    this.base = 0; // secuencia de lines[0]
    this.filter = null; // función línea → bool, o null para "todas"
    this.idx = []; // secuencias que pasan el filtro (solo si hay filtro)
  }

  get size() {
    return this.filter ? this.idx.length : this.lines.length;
  }

  // at devuelve la i-ésima línea de la vista.
  at(i) {
    return this.filter ? this.lines[this.idx[i] - this.base] : this.lines[i];
  }

  // push agrega líneas y devuelve cuántas filas de la vista se fueron por
  // arriba al descartar las viejas: el visor corre el scroll esa cantidad
  // para que lo que se está leyendo no salte.
  push(batch) {
    for (const l of batch) {
      l.seq = this.base + this.lines.length;
      this.lines.push(l);
      if (this.filter && this.filter(l)) this.idx.push(l.seq);
    }
    const slack = Math.max(1, this.cap >> 3);
    if (this.lines.length <= this.cap + slack) return 0;

    const drop = this.lines.length - this.cap;
    this.lines.splice(0, drop);
    this.base += drop;
    if (!this.filter) return drop;
    let k = 0;
    while (k < this.idx.length && this.idx[k] < this.base) k++;
    this.idx.splice(0, k);
    return k;
  }

  setFilter(fn) {
    this.filter = fn;
    this.idx = fn ? this.lines.filter(fn).map((l) => l.seq) : [];
  }

  clear() {
    this.lines = [];
    this.idx = [];
    this.base = 0;
  }
}
