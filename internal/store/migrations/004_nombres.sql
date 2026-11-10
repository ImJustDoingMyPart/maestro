-- Nombres puestos desde la interfaz ("Cambiar nombre" en una tarjeta). La
-- clave es la misma que la de ocultar: "service:<id>" o
-- "stack:<host>/<proyecto>". Pisan a los labels, no al archivo.

CREATE TABLE card_names (
    key  TEXT    PRIMARY KEY,
    name TEXT    NOT NULL,
    at   INTEGER NOT NULL -- ms Unix
) STRICT, WITHOUT ROWID;
