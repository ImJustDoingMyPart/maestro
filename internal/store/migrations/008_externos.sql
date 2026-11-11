-- Servicios externos creados desde la interfaz (un router, un sitio web):
-- no se descubren, se vigilan con un check HTTP o TCP. El push no: su token
-- es un secreto y va solo en el archivo. El nombre y el grupo van en
-- card_names y card_groups con la clave "service:<id>", como los de
-- cualquier tarjeta. Las duraciones en ms; 0 es el valor por defecto.

CREATE TABLE externals (
    id           TEXT    PRIMARY KEY,
    url          TEXT    NOT NULL, -- link de la tarjeta; puede ir vacío
    description  TEXT    NOT NULL,
    check_kind   TEXT    NOT NULL CHECK (check_kind IN ('http', 'tcp')),
    target       TEXT    NOT NULL,
    expect       INTEGER NOT NULL,
    no_redirects INTEGER NOT NULL CHECK (no_redirects IN (0, 1)),
    interval_ms  INTEGER NOT NULL,
    timeout_ms   INTEGER NOT NULL,
    created_at   INTEGER NOT NULL -- ms Unix
) STRICT, WITHOUT ROWID;
