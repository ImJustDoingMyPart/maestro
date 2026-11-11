-- "Sacar del stack" desde la interfaz: el equivalente de maestro.separate.
-- La clave es "service:<id>". separate 1 lo saca a una tarjeta propia y 0
-- lo devuelve a la del stack aunque un label diga lo contrario. Pisa a los
-- labels, no al archivo.

CREATE TABLE separated (
    key      TEXT    PRIMARY KEY,
    separate INTEGER NOT NULL CHECK (separate IN (0, 1)),
    at       INTEGER NOT NULL -- ms Unix
) STRICT, WITHOUT ROWID;
