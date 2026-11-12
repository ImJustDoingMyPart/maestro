-- Orden y ocultos de la pantalla Hardware, por host, hechos en su modo
-- edición. item es "tile:cpu", "chart:mem", "section:storage", …; una fila
-- sin posición (NULL) solo dice si está oculto. Lo que no tiene fila va
-- visible, después de lo ordenado y en el orden de siempre.

CREATE TABLE hw_layout (
    host     TEXT    NOT NULL,
    item     TEXT    NOT NULL,
    position INTEGER,
    hidden   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (host, item)
) STRICT, WITHOUT ROWID;
