-- Ícono elegido desde la interfaz para una tarjeta (la clave es la de
-- ocultar: "service:…", "stack:…", "found:…"). icon tiene el formato de
-- los labels ("adguard-home", "sh-aiostreams", "mdi-…", una URL).

CREATE TABLE card_icons (
    key  TEXT    PRIMARY KEY,
    icon TEXT    NOT NULL,
    at   INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

-- Preferencias sueltas de la interfaz: por ahora, el host que Hardware
-- muestra primero ("hw_pinned").
CREATE TABLE ui_prefs (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT, WITHOUT ROWID;
