-- Tarjetas ocultas en la portada. Es la primera configuración que se hace
-- desde la interfaz. La clave es "service:<id>" o "stack:<host>/<proyecto>".
-- Ocultar no deja de vigilar: solo no se muestra.

CREATE TABLE hidden (
    key TEXT    PRIMARY KEY,
    at  INTEGER NOT NULL -- ms Unix
) STRICT, WITHOUT ROWID;
