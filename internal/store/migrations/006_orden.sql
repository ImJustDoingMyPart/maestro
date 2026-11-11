-- Orden de las tarjetas dentro de su grupo, hecho arrastrando en el modo
-- edición. La clave es la de ocultar. Las tarjetas sin fila van después
-- de las ordenadas, por nombre.

CREATE TABLE card_order (
    key      TEXT    PRIMARY KEY,
    position INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
