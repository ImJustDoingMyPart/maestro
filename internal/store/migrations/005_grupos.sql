-- Grupos de la portada hechos desde la interfaz.
--
-- groups son los grupos que la interfaz conoce, en orden: los creados con
-- "Nuevo grupo" (aunque estén vacíos) y los que se ordenaron arrastrando.
-- Un grupo que solo sale de los labels no está acá hasta que se lo ordena.
--
-- card_groups dice a qué grupo va cada tarjeta; la clave es la de ocultar
-- ("service:<id>" o "stack:<host>/<proyecto>"). grp vacío es "Otros": no es
-- lo mismo que no tener fila, que deja el grupo de los labels. Pisa a los
-- labels, no al archivo.

CREATE TABLE groups (
    name     TEXT    PRIMARY KEY,
    position INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE TABLE card_groups (
    key TEXT    PRIMARY KEY,
    grp TEXT    NOT NULL,
    at  INTEGER NOT NULL -- ms Unix
) STRICT, WITHOUT ROWID;
