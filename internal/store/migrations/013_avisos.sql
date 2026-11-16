-- "Avisar por Telegram" desde la interfaz: el equivalente de maestro.alert.
-- La clave es la del servicio: "service:<id>" o, para lo descubierto que
-- se muestra, "found:<host>/<tipo>/<nombre>". alert 1 avisa y 0 no, aunque
-- un label o el valor por defecto digan lo contrario. Pisa a los labels,
-- no al archivo.

CREATE TABLE alerts (
    key   TEXT    PRIMARY KEY,
    alert INTEGER NOT NULL CHECK (alert IN (0, 1)),
    at    INTEGER NOT NULL -- ms Unix
) STRICT, WITHOUT ROWID;
