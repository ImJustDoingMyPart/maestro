-- Etapa 4: resultados de los checks, sus resúmenes por hora y los cambios
-- de estado de cada servicio. Las horas van en milisegundos Unix: la
-- latencia y el orden de dos resultados seguidos necesitan más que
-- segundos.

-- Un resultado por corrida de cada check. Se guardan unos días y después
-- quedan solo en check_hourly (ver Store.Compact).
CREATE TABLE check_results (
    service     TEXT    NOT NULL, -- ID del servicio en el catálogo
    kind        TEXT    NOT NULL, -- "http", "tcp" o "push"
    at          INTEGER NOT NULL, -- ms Unix
    ok          INTEGER NOT NULL CHECK (ok IN (0, 1)),
    latency_ms  INTEGER NOT NULL,
    detail      TEXT    NOT NULL,
    cert_expiry INTEGER          -- ms Unix; NULL si no aplica
) STRICT;

CREATE INDEX check_results_service_at ON check_results (service, kind, at);
CREATE INDEX check_results_at ON check_results (at);

-- Resumen por hora de los resultados que ya se compactaron.
CREATE TABLE check_hourly (
    service        TEXT    NOT NULL,
    kind           TEXT    NOT NULL,
    hour           INTEGER NOT NULL, -- ms Unix del comienzo de la hora
    total          INTEGER NOT NULL,
    ok             INTEGER NOT NULL,
    latency_sum_ms INTEGER NOT NULL, -- suma, no promedio: dos tandas de la misma hora se suman
    PRIMARY KEY (service, kind, hour)
) STRICT, WITHOUT ROWID;

-- Cambios de estado de un servicio. from_state es "" en el primero.
CREATE TABLE state_events (
    id         INTEGER PRIMARY KEY,
    service    TEXT    NOT NULL,
    at         INTEGER NOT NULL, -- ms Unix
    from_state TEXT    NOT NULL,
    to_state   TEXT    NOT NULL,
    reason     TEXT    NOT NULL
) STRICT;

CREATE INDEX state_events_service_at ON state_events (service, at);
