-- Etapa 7: series de tiempo de las métricas (host, contenedores, LXC y
-- VMs). Cada muestra se suma en tres resoluciones a la vez (1 min, 10 min
-- y 1 h); cada una se guarda un tiempo distinto (ver Store.CompactMetrics).

-- Una serie por clave: "h/<host>/cpu", "c/<host>/<contenedor>/mem", …
CREATE TABLE metric_series (
    id  INTEGER PRIMARY KEY,
    key TEXT    NOT NULL UNIQUE
) STRICT;

-- Un punto por serie, resolución y comienzo del intervalo. sum y n en vez
-- del promedio, para seguir sumando muestras al mismo intervalo.
CREATE TABLE metric_points (
    series INTEGER NOT NULL REFERENCES metric_series (id) ON DELETE CASCADE,
    res    INTEGER NOT NULL, -- segundos del intervalo: 60, 600 o 3600
    at     INTEGER NOT NULL, -- ms Unix del comienzo del intervalo
    sum    REAL    NOT NULL,
    n      INTEGER NOT NULL,
    max    REAL    NOT NULL,
    PRIMARY KEY (series, res, at)
) STRICT, WITHOUT ROWID;

CREATE INDEX metric_points_res_at ON metric_points (res, at);
