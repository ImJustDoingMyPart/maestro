-- Etapa 1: un usuario (la tabla admite más por si algún día hace falta) y
-- sus sesiones. De cada sesión se guarda el hash del token, nunca el token:
-- quien lea la base no puede entrar con lo que encuentre.

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL, -- argon2id en formato PHC
    created_at    INTEGER NOT NULL, -- segundos Unix
    updated_at    INTEGER NOT NULL
) STRICT;

CREATE TABLE sessions (
    token_hash BLOB    PRIMARY KEY, -- sha256 del token de la cookie
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX sessions_expires_at ON sessions (expires_at);
