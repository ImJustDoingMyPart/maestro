# Maestro

Panel único para un servidor chico administrado por una persona: una portada con el estado de cada servicio, descubierto solo desde Docker, más los que se declaran a mano. Con el tiempo suma logs en vivo, edición de compose, checks, métricas de hardware y alertas.

Es un solo binario de Go con dos modos:

- `maestro serve`: el **hub**. Web, login y base SQLite. No toca el socket de Docker: le pide los datos a los agents.
- `maestro agent`: uno por **host**. Lee Docker, edita y levanta los stacks de compose, y responde solo al hub, con un token.

Estado: etapa 4 (checks y estado). Hay portada con login y lista de servicios desde Docker; el semáforo combina el estado de los contenedores con checks HTTP, TCP y push que corren en el hub, y cada cambio de estado queda guardado. Cada servicio tiene su página con el historial de sus checks, la línea de tiempo (logs en vivo con los cambios de estado intercalados) y, si es de compose, la pestaña Compose para editar el `compose.yml` y el `.env` y correr `up`, `down`, `restart` y `pull`.

## Desarrollo

Requiere Go 1.27. No hace falta Node.

```sh
go vet ./... && go test -race ./...
go build -o maestro ./cmd/maestro
```

### JavaScript

El JavaScript propio está en `web/src/` y [esbuild](https://esbuild.github.io/) lo junta en `web/static/gen/`, que va commiteado para que `go build` alcance. esbuild está escrito en Go y figura como herramienta en `go.mod`, así que después de tocar `web/src/` se regenera con:

```sh
go generate ./web
```

Hay un bundle por página: `logs.js` (el visor) y `compose.js` (el editor, que solo se carga en la pestaña Compose). El editor usa [CodeMirror 6](https://codemirror.net/), que se publica en npm. Para no depender de Node, `go generate` corre antes `internal/npmfetch`, que baja a `web/node_modules/` (ignorado por git) las versiones fijas de `web/npm.lock` y verifica el sha512 de cada tarball. El lockfile lista todos los paquetes, también los transitivos; para agregar o actualizar uno:

```sh
go run ./internal/npmfetch -lock web/npm.lock -add @codemirror/view@6.43.15
```

Un test de `web` falla si el bundle commiteado no sale de `web/src/` tal como está (si no están los paquetes de npm, se saltea). La lógica pura del visor (niveles, buffer) tiene tests aparte que sí necesitan Node: `node --test web/src/`.

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
./maestro agent --listen 127.0.0.1:9101 --stacks-dir ~/docker
```

Con `--stacks-dir`, la pestaña Compose escribe en esa carpeta y `up`/`down` actúan sobre el Docker de `DOCKER_HOST`: contra el server real, eso es escribir en él. Para probar, mejor un stack de juguete en el Docker local con su propia carpeta.

El usuario remoto tiene que estar en el grupo `docker`. Hablar con el socket de Docker equivale a ser root en ese host.

### Hub

```sh
./maestro passwd --db maestro.db --user <usuario>   # pide la contraseña dos veces
export MAESTRO_TOKEN_VM="$MAESTRO_AGENT_TOKEN"
./maestro serve --dev --config maestro.yaml --db maestro.db
```

`--dev` deja la cookie de sesión sin `Secure` para poder entrar por `http://`. Sin `--dev`, el hub espera estar detrás de un proxy con HTTPS.

## Página de un servicio

Tocar una tarjeta de la portada lleva a `/services/<id>` (por ejemplo `/services/media-stack/jellyfin`). Muestra de dónde sale el servicio, su host y sus contenedores, y la pestaña Actividad con los logs en vivo.

### Logs

- Al abrir llegan las últimas 1000 líneas y después las nuevas, por Server-Sent Events (`GET /logs/<id>`). Si el servicio tiene réplicas, sus líneas van intercaladas por hora y cada una dice de qué contenedor es; un selector deja ver una sola.
- Si la conexión se corta, el navegador reconecta solo y el stream sigue desde la última línea recibida, sin repetir ni perder. Cuando el contenedor se detiene, el visor lo avisa y la conexión sigue abierta: llegan los cambios de estado y, cuando el contenedor vuelve a arrancar (el mismo o uno recreado), los logs siguen desde la última línea. Si la lectura se corta por un error, el visor ofrece reintentar.
- Un contenedor que escribe más de 2000 líneas por segundo no ahoga al navegador: las de más se omiten y se avisa cuántas.
- El visor guarda las últimas 10 000 líneas y dibuja solo las que se ven. Detecta el nivel de cada línea (`ERROR`, `level=warn`, `"level":"error"`, `[error]`, los niveles numéricos de pino) para filtrar por avisos o errores, busca sin distinguir mayúsculas y saca los códigos de color ANSI.
- Al subir para leer se pausa el avance automático (mientras tanto guarda hasta 50 000 líneas para que lo que se lee no se borre); el botón del costado vuelve al final. Tocar una línea la muestra entera, con la fecha completa y el JSON indentado.

## Checks y estado

Los checks corren en el hub, cada uno en su goroutine. Un servicio tiene como mucho uno de cada tipo:

- **HTTP**: un `GET` a la URL. Vale cualquier 2xx, o el código de `expect`. Sigue hasta 10 redirecciones salvo `redirects: false` (entonces el 3xx es la respuesta). Cada vez abre una conexión nueva y no usa el proxy del entorno. En HTTPS guarda el vencimiento del certificado.
- **TCP**: conecta a `host:puerto` y cierra.
- **Push**: lo hace un script al terminar bien, con `curl https://<maestro>/push/<token>` (GET o POST, sin login). Si no llega en `interval` más un 10 %, falla, y sigue fallando una vez por intervalo mientras no llegue. Al reiniciar el hub, el plazo se cuenta desde el último push guardado. La ruta responde `ok` exista o no el token, para no servir de oráculo; los tokens desconocidos quedan en el log.

Por defecto, cada 60 s con 10 s de timeout. El timeout nunca pasa del intervalo. Los checks salen del contenedor del hub: en el despliegue de ejemplo está en `caddy_net`, así que `http://jellyfin:8096` resuelve por el DNS de Docker si el servicio está en esa red (igual que en Uptime Kuma).

El estado de un servicio:

| Estado | Cuándo |
|---|---|
| Caído | un contenedor no corre; un check HTTP o TCP falló 3 veces seguidas (como Uptime Kuma con 3 reintentos); un push no llegó |
| Degradado | un contenedor está `unhealthy` o reinició hace menos de 10 minutos; un check falló menos de 3 veces; un certificado vence en menos de 14 días |
| Sin datos | el agent de su host no responde y no hay checks con resultados; un contenedor está arrancando (`starting`); un servicio propio cuyos checks todavía no corrieron |
| Arriba | ninguna de las anteriores |

Cada cambio de estado se guarda con su motivo (`check http falla: timeout (10s)`, `jellyfin detenido (exit 137)`, `volvió a responder`). No tener datos no es un cambio: si el agent deja de responder y vuelve con todo igual, no queda nada. Un reinicio es una hora de arranque nueva en un contenedor que estaba corriendo; si estaba detenido, volver a levantarlo es un arranque y no cuenta. Docker borra el exit code al arrancar, así que el motivo de un reinicio lleva el exit code solo si el hub llegó a ver el contenedor en `restarting`.

Los resultados se guardan 7 días; después quedan resúmenes por hora (cantidad, buenos y latencia media) durante un año. La compactación corre cada hora. La base se crea con permisos `600`, y una base con permisos más abiertos se cierra al abrirla.

En la página del servicio, cada check muestra su último resultado, la latencia, el certificado, la disponibilidad de las últimas 24 horas y una barra por media hora. Se refresca cada minuto. La pestaña Actividad intercala los cambios de estado con las líneas de log; un servicio sin logs (externo) muestra la lista. Las horas se ven en la zona del navegador.

## Pestaña Compose

`/stacks/<host>/<proyecto>`, enlazada desde la página de cada servicio de compose. Se nombra por stack y no por servicio porque después de un `down` el stack no tiene contenedores ni servicios, y hay que poder levantarlo de nuevo: el agent lo encuentra igual por `<stacks-dir>/<proyecto>/compose.yml`.

- **Editor** con resaltado de YAML y de `.env`, indentación, plegado y buscar y reemplazar (`Ctrl+F`). `Ctrl+S` guarda. Si hay cambios sin guardar, el navegador avisa antes de salir.
- **Guardar** valida primero con `docker compose config` sobre una copia temporal al lado del archivo (las rutas relativas del compose se resuelven igual). Si falla, no se escribe nada y el error de compose se muestra debajo del editor. Si pasa, se muestra el diff y se pide confirmar.
- La escritura es atómica (temporal en la misma carpeta y `rename`) y conserva permisos y dueño; un `.env` nuevo se crea con `0600`. Si el archivo cambió en el disco desde que se abrió la página, no se pisa: se avisa.
- Después de escribir, si la carpeta es parte de un repo de git y el archivo no está ignorado, se hace un commit **solo de ese archivo** (`<proyecto>: compose.yml editado desde Maestro`), con el autor de `--git-author`. Sin push. Si no hay repo o el archivo está en el `.gitignore` (lo normal para el `.env`), se guarda igual y se avisa.
- **Acciones**: `up` (`up -d`), `down` (pide confirmación), `restart` y `pull`, sobre el stack entero. La salida llega en vivo al mismo visor de los logs. Hay una operación por stack a la vez: otra acción o un guardado en el medio se rechazan.
- Una acción no se corta si se cierra la página: un `up` a medias deja el stack roto. El hub sigue leyendo la salida y la guarda una hora; al volver a la página en los 10 minutos siguientes, se ve.
- `docker compose` corre con el entorno del agent menos las variables `MAESTRO_*` (el token no queda al alcance de la interpolación).

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
| `maestro.check.http.expect` | código HTTP esperado | | cualquier 2xx |
| `maestro.check.http.redirects` | `false`: no seguir redirecciones | | las sigue |
| `maestro.check.tcp` | `host:puerto` de un check TCP | | sin check |
| `maestro.check.interval` | cada cuánto, duración de Go (`30s`, `5m`) | | `60s` |
| `maestro.check.timeout` | cuánto esperar la respuesta | | `10s` |
| `maestro.alert` | `true`/`false`: avisar por Telegram | | `true` si tiene checks |
| `maestro.ignore` | `true`: no mostrarlo | | se muestra |

Una clave `maestro.*` desconocida o un valor inválido se ignora y queda en el log una vez. El check push no tiene label: su token es un secreto y va en `maestro.yaml`.

### `maestro.yaml`

Ver [`maestro.example.yaml`](maestro.example.yaml). Tiene dos secciones:

- `hosts`: cada host con agent, con `agent` (URL) y `token`.
- `services`: con clave `<proyecto>/<servicio>` pisa valores de un servicio descubierto; con una clave sin `/` declara uno propio, que es una unidad systemd si tiene `unit` (y entonces necesita `host`) o un servicio externo si no.

| Clave de un servicio | Tipo | Dónde vale |
|---|---|---|
| `name`, `group`, `icon`, `url`, `description` | texto | todos |
| `check` | `{http, expect, redirects, tcp, push, interval, timeout}` (ver abajo) | todos |
| `alert`, `ignore` | `true`/`false` | todos |
| `host` | nombre de un host de `hosts` | solo propios |
| `unit` | unidad systemd | solo propios |

`check` lleva al menos uno de `http` (URL), `tcp` (`host:puerto`) o `push` (token, de al menos 16 caracteres y distinto en cada servicio), y opcionalmente `expect` y `redirects` (de `http`) e `interval` y `timeout` (duraciones de Go, para todos los del servicio). Para un push, `interval` es cada cuánto tiene que llegar la llamada. Un `check` en el archivo reemplaza a los de los labels.

Una clave desconocida es un error al arrancar, igual que una variable `${VAR}` que no existe en el entorno. Los secretos van siempre en variables, nunca escritos en el archivo. Dentro de un mapa entre llaves (`{ ... }`), la variable va entre comillas: `token: "${MAESTRO_TOKEN_VM}"`.

## Despliegue

`deploy/compose.yml` es un ejemplo con los dos contenedores en el mismo host:

- `hub` en la red del proxy (por ejemplo, `caddy_net`), sin socket de Docker.
- `agent` con `network_mode: host`, el socket montado y el grupo `docker` del host por `group_add`, y la carpeta de los stacks montada **en la misma ruta que en el host**: los labels de compose dan la ruta del host, y `docker compose` le pasa al daemon las rutas de los bind mounts tal cual. Corre con el uid y el gid del dueño de esa carpeta (`user:`), para que lo que escribe y los commits queden como suyos.

Son dos imágenes del mismo `Dockerfile`: el hub (por defecto, `distroless/static`) y el agent (`--target agent`, Alpine con el CLI de Docker, el plugin de compose y git). Los dos corren como usuario sin privilegios, con el sistema de archivos de solo lectura y sin capacidades. El primer usuario se crea con:

```sh
docker compose run --rm hub passwd --db /data/maestro.db --user <usuario>
```

## Seguridad

- Login desde la primera versión. Las contraseñas se guardan como hashes argon2id. La sesión es un token aleatorio en una cookie `HttpOnly`, `Secure` y `SameSite=Lax`, y en la base solo queda su hash.
- Los POST de otro origen se rechazan con `http.CrossOriginProtection`.
- El agent no se publica y exige un token Bearer. El token se compara en tiempo constante y no se muestra en los logs.
- Montar el socket con `:ro` no hace que la API de Docker sea de solo lectura. El agent levanta y baja stacks y edita sus compose: quien controle el agent (o el hub, que le habla con el token) tiene el equivalente a root en ese host. Por eso el login, el agent sin publicar y el acceso solo detrás del proxy.
- El agent solo lee y escribe el `compose.yml` y el `.env` de un proyecto, y solo dentro de `--stacks-dir`: una ruta que, resueltos los symlinks, cae afuera se rechaza.
- El `.env` viaja del agent al navegador para editarlo: lleva secretos, y por eso esa página solo existe con sesión iniciada.
- Las líneas de log se insertan como texto, nunca como HTML: un contenedor no puede meter marcado en la página.
- `/push/<token>` no pide login: el token es la credencial. Se compara en tiempo constante contra todos los tokens, sin cortar en el primero, y la respuesta es la misma exista o no. El token viaja en la URL, así que queda en el log de acceso del proxy si está activado.
