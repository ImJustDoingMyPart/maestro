# Informe de Jellyfin

Generado por Maestro el 2026-11-11 15:04:05. Las horas están en UTC. Los secretos del compose, del `.env` y de los logs están reemplazados por `[censurado]`.

## Qué es

- **ID**: `media-stack/jellyfin`
- **Descripción**: Películas y series
- **Grupo**: Media
- **URL**: https://jellyfin.example.org
- **Origen**: compose · media-stack
- **Host**: vm

## Estado: Degradado

Desde 2026-11-11 14:58:00.

- check http falla: timeout (10s)
- jellyfin-1 reiniciado (exit 137)

### Contenedores

| Contenedor | Imagen | Estado | Health | Exit code | Arrancó | Reinicios |
|---|---|---|---|---|---|---|
| jellyfin-1 | jellyfin/jellyfin:10.11 | running | healthy | 137 | 2026-11-11 14:58:00 | 1 |
| jellyfin-2 | jellyfin/jellyfin:10.11 | running | — | — | 2026-11-11 09:00:00 | 0 |

## Checks (últimas 24 h)

| Check | Objetivo | Último resultado | Hora | Latencia | Disponibilidad | Certificado vence |
|---|---|---|---|---|---|---|
| HTTP | https://jellyfin.example.org/health | falla: timeout (10s) | 2026-11-11 15:04:00 | — | 99,3 % | 2027-01-20 |
| Push | — | ok | 2026-11-11 03:00:12 | — | 100 % | — |

### Últimas fallas de HTTP

- 2026-11-11 15:04:00 · timeout (10s)
- 2026-11-11 15:03:00 · HTTP 502 | Bad Gateway

## Cambios de estado (últimas 24 h)

- 2026-11-11 02:00:00 · Arriba → Caído · check http falla: timeout (10s)
- 2026-11-11 02:05:00 · Caído → Arriba
- 2026-11-11 14:58:00 · Arriba → Degradado · jellyfin-1 reiniciado (exit 137)

## Logs

Últimas 200 líneas de cada contenedor: 1 error y 1 aviso. `E` marca los errores y `W` los avisos.

```text
  2026-11-11 14:58:01 [jellyfin-1] [INF] Jellyfin listo
W 2026-11-11 14:58:02 [jellyfin-2] WARN slow query
E 2026-11-11 15:03:00 [jellyfin-1] ERROR db: password=[censurado] rejected
  [jellyfin-1] línea sin hora
```

## compose.yml

`/home/anon/docker/media-stack/compose.yml`

```yaml
services:
  jellyfin:
    image: jellyfin/jellyfin:10.11
    environment:
      - API_KEY=[censurado]
```

## .env

`/home/anon/docker/media-stack/.env`

```dotenv
TZ=UTC
DB_PASSWORD=[censurado]
```
