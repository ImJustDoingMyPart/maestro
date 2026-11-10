# Maestro

Panel único para un servidor chico administrado por una persona: una portada con el estado de cada servicio, descubierto solo desde Docker, más los que se declaran a mano. Con el tiempo suma logs en vivo, edición de compose, checks, métricas de hardware y alertas.

Es un solo binario de Go con dos modos:

- `maestro serve`: el **hub**. Web, login y base SQLite. No toca el socket de Docker: le pide los datos a los agents.
- `maestro agent`: uno por **host**. Lee Docker y responde solo al hub, con un token.

Estado: etapa 1 (esqueleto). Hay portada con login y lista de servicios desde Docker; el semáforo refleja el estado de los contenedores. Los checks HTTP se leen de la configuración pero todavía no se ejecutan.

## Desarrollo

Requiere Go 1.27. No hace falta Node.

```sh
go vet ./... && go test -race ./...
go build -o maestro ./cmd/maestro
```

### Portada de prueba

Sin agents ni login, con servicios inventados:

```sh
./maestro serve --demo --listen 127.0.0.1:8090
```

### Agent contra un server remoto

El SDK de Docker para Go no entiende `DOCKER_HOST=ssh://`, así que el socket remoto se trae con un túnel de SSH:

```sh
ssh -nNT -o StreamLocalBindUnlink=yes \
    -L "$XDG_RUNTIME_DIR/maestro-docker.sock:/var/run/docker.sock" <server> &

export DOCKER_HOST="unix://$XDG_RUNTIME_DIR/maestro-docker.sock"
export MAESTRO_AGENT_TOKEN="$(openssl rand -hex 32)"   # mínimo 32 caracteres
./maestro agent --listen 127.0.0.1:9101
```

El usuario remoto tiene que estar en el grupo `docker`. Hablar con el socket de Docker equivale a ser root en ese host.

### Hub

```sh
./maestro passwd --db maestro.db --user <usuario>   # pide la contraseña dos veces
export MAESTRO_TOKEN_VM="$MAESTRO_AGENT_TOKEN"
./maestro serve --dev --config maestro.yaml --db maestro.db
```

`--dev` deja la cookie de sesión sin `Secure` para poder entrar por `http://`. Sin `--dev`, el hub espera estar detrás de un proxy con HTTPS.

## Configuración

Lo que se puede descubrir no se configura: todo contenedor aparece solo. Un servicio se ajusta con labels en su `compose.yml` o con el archivo `maestro.yaml` del hub. Prioridad, de mayor a menor: `maestro.yaml`, labels `maestro.*`, labels `homepage.*` y nombres de compose. Los labels de Homepage ya existentes se aprovechan sin migrar nada.

### Identidad de un servicio

Un servicio de compose se identifica como `<proyecto>/<servicio>`, por ejemplo `media-stack/jellyfin` o `immich/database`. Las réplicas de un mismo servicio de compose son un solo servicio. Un contenedor sin compose se identifica por su nombre.

### Labels

| Label | Qué hace | Equivalente de Homepage | Si falta |
|---|---|---|---|
| `maestro.name` | nombre visible | `homepage.name` | servicio de compose |
| `maestro.group` | grupo de la portada | `homepage.group` | proyecto de compose |
| `maestro.icon` | ícono | `homepage.icon` | genérico |
| `maestro.url` | link "Abrir" | `homepage.href` | sin link |
| `maestro.description` | texto bajo el nombre | `homepage.description` | imagen del contenedor |
| `maestro.check.http` | URL de un check HTTP | | sin check |
| `maestro.check.http.expect` | código HTTP esperado | | cualquier 2xx (etapa 4) |
| `maestro.alert` | `true`/`false`: avisar por Telegram | | `true` si tiene checks |
| `maestro.ignore` | `true`: no mostrarlo | | se muestra |

Una clave `maestro.*` desconocida o un valor inválido se ignora y queda en el log una vez.

### `maestro.yaml`

Ver [`maestro.example.yaml`](maestro.example.yaml). Tiene dos secciones:

- `hosts`: cada host con agent, con `agent` (URL) y `token`.
- `services`: con clave `<proyecto>/<servicio>` pisa valores de un servicio descubierto; con una clave sin `/` declara uno propio, que es una unidad systemd si tiene `unit` (y entonces necesita `host`) o un servicio externo si no.

| Clave de un servicio | Tipo | Dónde vale |
|---|---|---|
| `name`, `group`, `icon`, `url`, `description` | texto | todos |
| `check` | `{http: <url>, expect: <código>}` | todos |
| `alert`, `ignore` | `true`/`false` | todos |
| `host` | nombre de un host de `hosts` | solo propios |
| `unit` | unidad systemd | solo propios |

Una clave desconocida es un error al arrancar, igual que una variable `${VAR}` que no existe en el entorno. Los secretos van siempre en variables, nunca escritos en el archivo. Dentro de un mapa entre llaves (`{ ... }`), la variable va entre comillas: `token: "${MAESTRO_TOKEN_VM}"`.

## Despliegue

`deploy/compose.yml` es un ejemplo con los dos contenedores en el mismo host:

- `hub` en la red del proxy (por ejemplo, `caddy_net`), sin socket de Docker.
- `agent` con `network_mode: host`, el socket montado y el grupo `docker` del host por `group_add`.

Los dos corren como usuario sin privilegios, con el sistema de archivos de solo lectura y sin capacidades. El primer usuario se crea con:

```sh
docker compose run --rm hub passwd --db /data/maestro.db --user <usuario>
```

## Seguridad

- Login desde la primera versión. Las contraseñas se guardan como hashes argon2id. La sesión es un token aleatorio en una cookie `HttpOnly`, `Secure` y `SameSite=Lax`, y en la base solo queda su hash.
- Los POST de otro origen se rechazan con `http.CrossOriginProtection`.
- El agent no se publica y exige un token Bearer. El token se compara en tiempo constante y no se muestra en los logs.
- Montar el socket con `:ro` no hace que la API de Docker sea de solo lectura. Lo que acota al agent es su código, que en esta etapa solo lista e inspecciona contenedores.
