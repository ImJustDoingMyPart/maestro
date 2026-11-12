# Maestro

Panel único para un servidor chico administrado por una persona: una portada con el estado de cada servicio, descubierto solo desde Docker, más los que se declaran a mano. Con el tiempo suma logs en vivo, edición de compose, checks, métricas de hardware y alertas.

Es un solo binario de Go con dos modos:

- `maestro serve`: el **hub**. Web, login y base SQLite. No toca el socket de Docker: le pide los datos a los agents.
- `maestro agent`: uno por **host**. Lee Docker (si hay), `/proc`, `/sys` y systemd, edita y levanta los stacks de compose, y responde solo al hub, con un token.

Estado: etapa 7 (hardware, métricas y descubrimiento). Además de lo de abajo, cada host con agent tiene su hardware (CPU, RAM, red, discos, temperaturas, SMART) con historial, cada servicio de Docker su pestaña Métricas, y las unidades y timers de systemd y las LXC y VMs de Proxmox llegan a la bandeja de Descubiertos. Lo de etapas anteriores: Hay portada con login y lista de servicios desde Docker; el semáforo combina el estado de los contenedores con checks HTTP, TCP y push que corren en el hub, y cada cambio de estado queda guardado. Cada servicio tiene su página con el historial de sus checks, la línea de tiempo (logs en vivo con los cambios de estado intercalados) y, si es de compose, la pestaña Compose para editar el `compose.yml` y el `.env` y correr `up`, `down`, `restart` y `pull`. Cada servicio da además un informe en Markdown, con los secretos censurados, para pegarlo en un agente de IA.

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

## Portada

Tarjetas por grupo. Los grupos van en el orden que se les dio (arrastrando o con `groups:` en el archivo) y después por nombre, sin distinguir mayúsculas, con "Otros" (las tarjetas sin grupo) siempre al final; las tarjetas, en el orden en que se las arrastró y después por nombre. Se refresca cada 15 segundos.

- **Todas las tarjetas tienen la misma forma.** Arriba el logo, el nombre y una segunda línea (el motivo si algo anda mal, si no la descripción); abajo el estado y cuatro botones siempre en el mismo lugar: servicios del stack, Métricas, Compose y Abrir. El que no aplica a esa tarjeta queda apagado en su lugar, así dos tarjetas no se ven distintas por tener distintas cosas. Nombre y segunda línea se cortan con "…" (el texto entero queda al pasar el mouse): el contenido no cambia el tamaño de la tarjeta.
- **Un stack, una tarjeta.** Un proyecto de compose con más de un servicio es una sola tarjeta (Immich, no sus cinco contenedores). Si uno solo de sus servicios tiene link, la tarjeta toma su nombre, grupo, logo y link; si no, el nombre del proyecto y el primer grupo que declare alguno de sus servicios. Su semáforo es el peor de sus servicios, con el motivo de ese servicio. Al tocarla (o su botón con la cantidad de servicios) se despliegan sus servicios, cada uno con su semáforo y el link a su página. La portada recuerda qué tarjetas están desplegadas mientras dure la pestaña del navegador. Una tarjeta de un servicio suelto lleva a su página.
- **Sacar del stack.** Un servicio sale de la tarjeta de su stack a una propia con "Sacar del stack" en su menú, con `maestro.separate: "true"` o con `separate: true` en el archivo; "Volver al stack" lo devuelve.
- **Menú de tres puntos** arriba a la derecha de cada tarjeta, y en cada servicio dentro de un stack: **Cambiar nombre**, **Mover a grupo** (uno que no existe se crea; vacío va a Otros; "Grupo original" vuelve al de los labels), **Sacar del stack** o **Volver al stack**, **Editar** (solo los servicios externos creados desde la interfaz) y **Ocultar**. Mientras un menú o un diálogo están abiertos, la portada no se refresca.
- **Ocultar.** Ocultar solo la saca de la portada: el estado se sigue calculando y la página del servicio sigue existiendo. `/hidden` lista lo oculto, con un botón para mostrarlo de nuevo. Es distinto de `maestro.ignore`, que saca al servicio del catálogo.
- **Modo edición** (botón "Editar" arriba): las tarjetas se arrastran dentro de su grupo y de un grupo a otro, y los grupos por su manija; en el celular, manteniendo apretado un momento. Cada grupo tiene botones para cambiarle el nombre (si ya existe uno con el nombre nuevo, los dos quedan en uno) y borrarlo (sus tarjetas pasan a Otros), y abajo están "Nuevo grupo" (aparece vacío al final), "Agregar servicio externo", "Descubiertos", "Ocultos" y "Exportar a maestro.yaml". Mientras dura, la portada no se refresca ni los links navegan.
- **Servicios externos** (`/externals/new`): lo que no se descubre (el router, un sitio web), con un check HTTP o TCP validado igual que en el archivo. El check push queda solo en el archivo: su token es un secreto. Tampoco se aceptan URLs con usuario y contraseña.
- **Descubiertos** (`/discovered`): las unidades y timers de systemd y las LXC y VMs de Proxmox que encuentran los agents no van solos a la portada; "Mostrar" las pasa. Ver "Descubiertos" abajo.
- **Exportar** (`/export`, o `/export.yaml` para bajarlo): lo configurado desde la interfaz como secciones de `maestro.yaml`, para versionarlo y rearmar el hub sin perderlo. No trae hosts ni tokens: la interfaz no guarda secretos. El orden de las tarjetas dentro de cada grupo no se exporta.

Todo lo que se configura desde la interfaz se guarda en la base del hub y pisa a los labels, pero no al archivo: si `maestro.yaml` fija un nombre, un grupo, `separate`, `hidden` o el orden de los grupos, el control correspondiente queda deshabilitado (y el hub responde 409 si se lo pide igual).

### Logos

Cada tarjeta lleva el logo de su aplicación, de [dashboard-icons](https://github.com/homarr-labs/dashboard-icons) (los mismos que usa Homepage, unos 3800). El navegador nunca le pide nada a un tercero: pide `/icons/…` al hub, que baja cada logo de jsDelivr la primera vez y lo guarda en `icons/`, al lado de la base. El índice de dashboard-icons (`metadata.json`) también queda ahí y se renueva una vez por semana; sin Internet, el hub usa lo que ya tiene y las tarjetas sin logo llevan un ícono genérico.

- **Sin configurar nada.** El hub busca el logo por el nombre del servicio, el servicio y el proyecto de compose, la imagen (`ghcr.io/immich-app/immich-server` da `immich`; los que reempaquetan, como `linuxserver` o `jlesage`, no cuentan) y la unidad de systemd (`AdGuardHome.service` da `adguard-home`). Ignora mayúsculas, guiones y palabras de relleno al final (`-server`, `-app`, `-web`) y, como último recurso, prueba con la primera palabra (`restic-backup.timer` da `restic`).
- **Declarado** con `maestro.icon`, `homepage.icon` o `icon:` en el archivo, en el formato de Homepage: `immich` o `immich.png` (dashboard-icons), `mdi-dumbbell` ([Material Design Icons](https://pictogrammers.com/library/mdi/)), `si-github` ([Simple Icons](https://simpleicons.org)) o una URL. Un nombre que no es de dashboard-icons se toma como ícono de [Material Symbols](https://fonts.google.com/icons) (`shield`, `router`).
- Los logos con variante clara y oscura siguen el tema del sistema. Cuál es cuál lo decide el hub midiendo los colores de cada SVG, porque dashboard-icons no nombra siempre igual sus variantes.

## Página de un servicio

Tocar una tarjeta de la portada lleva a `/services/<id>` (por ejemplo `/services/media-stack/jellyfin`). Muestra de dónde sale el servicio, su host y sus contenedores, y la pestaña Actividad con los logs en vivo.

### Logs

- Al abrir llegan las últimas 1000 líneas y después las nuevas, por Server-Sent Events (`GET /logs/<id>`). Si el servicio tiene réplicas, sus líneas van intercaladas por hora y cada una dice de qué contenedor es; un selector deja ver una sola.
- Si la conexión se corta, el navegador reconecta solo y el stream sigue desde la última línea recibida, sin repetir ni perder. Cuando el contenedor se detiene, el visor lo avisa y la conexión sigue abierta: llegan los cambios de estado y, cuando el contenedor vuelve a arrancar (el mismo o uno recreado), los logs siguen desde la última línea. Si la lectura se corta por un error, el visor ofrece reintentar.
- Un contenedor que escribe más de 2000 líneas por segundo no ahoga al navegador: las de más se omiten y se avisa cuántas.
- El visor guarda las últimas 10 000 líneas y dibuja solo las que se ven. Detecta el nivel de cada línea (`ERROR`, `level=warn`, `"level":"error"`, `[error]`, los niveles numéricos de pino) para filtrar por avisos o errores, busca sin distinguir mayúsculas y saca los códigos de color ANSI.
- Al subir para leer se pausa el avance automático (mientras tanto guarda hasta 50 000 líneas para que lo que se lee no se borre); el botón del costado vuelve al final. Tocar una línea la muestra entera, con la fecha completa y el JSON indentado.

## Hardware y métricas

El agent toma una muestra cada 15 segundos y el hub guarda cada una (una sola vez, aunque la pida dos) como series de tiempo en su base.

- **Hardware** (`/hardware`): un host por vez, con una pestaña por host (`/hardware?host=pve`). Arriba, la CPU, la RAM, el swap, la carga (sobre la cantidad de hilos) y la temperatura más alta en el momento, cada una con su barra; gráficos de CPU, memoria, red (por interfaz), discos (lectura y escritura por disco entero) y temperaturas (las ocho más altas de hwmon); el espacio de cada sistema de archivos; el SMART de los discos; en un Proxmox, sus LXC y VMs con su CPU y RAM; y los contenedores que más memoria usan, con link a su servicio.
- **Modo edición de Hardware** (botón "Editar"): los mosaicos y los gráficos se ordenan arrastrando y cualquier mosaico, gráfico o sección (Almacenamiento, LXC y VMs, Contenedores) se oculta con el ojo. Vale por host y se guarda en la base del hub; lo oculto se ve apagado mientras se edita, para poder mostrarlo de nuevo.
- Los mosaicos y las tarjetas que quedan solos en su fila van centrados; un gráfico solo en su fila ocupa el ancho entero.
- Un gráfico tiene como mucho ocho líneas, una por color de una paleta fija validada para daltonismo (con tonos propios para el tema oscuro). La leyenda muestra el último valor de cada línea y, con el mouse encima, el de ese momento.
- **Métricas** (pestaña de la página de un servicio de Docker, `/metrics/<id>`): CPU, memoria, red y disco de cada contenedor, una línea por réplica. Los números son los de `docker stats`: la CPU en porcentaje de un núcleo (200 % son dos núcleos llenos) y la memoria sin la caché de archivos inactiva.
- Los gráficos son de [uPlot](https://github.com/leeoniya/uPlot); el rango (1 h a 1 año) se elige arriba, vale para toda la página y el navegador lo recuerda. Se refrescan cada minuto. Un hueco en los datos (el agent caído) corta la línea en vez de unir los extremos. `GET /data/metrics?range=24h&key=…` da los datos, con sesión.
- Cada muestra se suma en tres resoluciones: 1 minuto (se guarda 26 horas), 10 minutos (15 días) y 1 hora (400 días; 90 para los contenedores, que son muchas más series). Hasta 24 h se dibuja por minuto, 7 días cada 10 minutos y más, por hora; cada punto es el promedio del intervalo (también se guarda el máximo).
- Las interfaces de red que crean Docker y Proxmox para cada contenedor o invitado (`veth`, `tap`, `fwbr`, `docker0`, `br-…`) no se muestran: van y vienen, y su tráfico ya pasa por la interfaz física o el puente principal. Los sistemas de archivos son los de disco (ext4, xfs, btrfs, zfs…), uno por dispositivo. Dos sensores con el mismo chip y nombre (dos módulos de RAM iguales) llevan " (2)".
- **SMART** (`--smart`, como root): `smartctl -n standby` cada 10 minutos, así un disco en reposo no se despierta para leerlo. Se muestra la autoevaluación, la temperatura, las horas encendido y los sectores reasignados, pendientes e incorregibles (en NVMe, los errores de medio); con sectores malos el disco sale degradado aunque la autoevaluación pase. Una VM no ve el SMART de los discos físicos: lo lee el agent del host de Proxmox.

## Descubiertos

El agent lee por D-Bus (el bus del sistema, que deja leer sin ser root) los servicios y timers de systemd cargados y, con `--proxmox` en un host de Proxmox, las LXC y VMs con `pvesh` cada 30 segundos. Todo va a la bandeja (`/discovered`), por host, con lo que falla primero y un filtro.

- **Estado**: parar no es fallar. Un servicio `active` está arriba; `failed` está caído con el motivo (`falló (exit 1)`); `inactive` está **detenido** (punto gris hueco), no caído: muchas unidades del sistema corren una vez y quedan así; arrancando o deteniéndose, sin datos. Un timer se juzga por cómo terminó la última corrida del servicio que dispara (`última corrida falló (exit 1)`) y está detenido si el timer mismo no está activo. Una LXC o VM prendida está arriba, apagada está detenida y pausada, degradada. Un script con timer (Restic) se vigila así, sin push.
- **Mostrar** la pasa a la portada como un servicio más: se renombra, se mueve y se ordena como cualquier tarjeta, sus cambios de estado se guardan y tiene su página (`/services/found:<host>/<tipo>/<nombre>`, por ejemplo `found:vm/timer/restic-backup.timer` o `found:pve/lxc/102`; las LXC y VMs se nombran por VMID, que no cambia). "Volver a la bandeja" (o "Ocultar") la saca.
- **Una unidad que falla avisa aunque no esté en la portada**: arriba de la portada aparece cuántas hay, con link a la bandeja. Las detenidas no avisan.
- Una unidad declarada en el archivo (`unit` y `host` en `services`) toma su estado del mismo lugar; si el agent responde y no la tiene, sale caída ("no existe en el host").
- El journal de las unidades todavía no se muestra.

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

Cada cambio de estado se guarda con su motivo (`check http falla: timeout (10s)`, `jellyfin detenido (exit 137)`, `volvió a responder`). No tener datos no es un cambio: si el agent deja de responder y vuelve con todo igual, no queda nada. Un reinicio es una hora de arranque nueva en un contenedor que estaba corriendo; si estaba detenido, volver a levantarlo es un arranque y no cuenta. Docker borra el exit code al arrancar, así que el agent escucha los eventos `die` de Docker y manda el último exit code de cada contenedor: con eso el motivo dice `reiniciado (exit 137)`. Al arrancar el hub, un contenedor que terminó y volvió a arrancar en menos de un minuto cuenta como reiniciado aunque el hub no lo haya visto.

Los resultados se guardan 7 días; después quedan resúmenes por hora (cantidad, buenos y latencia media) durante un año. La compactación corre cada hora. La base se crea con permisos `600`, y una base con permisos más abiertos se cierra al abrirla.

En la página del servicio, cada check muestra su último resultado, la latencia, el certificado, la disponibilidad de las últimas 24 horas y una barra por media hora. Se refresca cada minuto; el semáforo, los contenedores y los motivos, cada 15 segundos. La disponibilidad de un push es por tiempo: cada llamada cubre su intervalo más la gracia, y cuenta la parte del día que estuvo cubierta. La pestaña Actividad intercala los cambios de estado con las líneas de log; un servicio sin logs (externo) muestra la lista. Las horas se ven en la zona del navegador.

## Pestaña Compose

`/stacks/<host>/<proyecto>`, enlazada desde la página de cada servicio de compose. Se nombra por stack y no por servicio porque después de un `down` el stack no tiene contenedores ni servicios, y hay que poder levantarlo de nuevo: el agent lo encuentra igual por `<stacks-dir>/<proyecto>/compose.yml`.

- **Editor** con resaltado de YAML y de `.env`, indentación, plegado y buscar y reemplazar (`Ctrl+F`). `Ctrl+S` guarda. Si hay cambios sin guardar, el navegador avisa antes de salir.
- **Guardar** valida primero con `docker compose config` sobre una copia temporal al lado del archivo (las rutas relativas del compose se resuelven igual). Si falla, no se escribe nada y el error de compose se muestra debajo del editor. Si pasa, se muestra el diff y se pide confirmar.
- La escritura es atómica (temporal en la misma carpeta y `rename`) y conserva permisos y dueño; un `.env` nuevo se crea con `0600`. Si el archivo cambió en el disco desde que se abrió la página, no se pisa: se avisa.
- Después de escribir, si la carpeta es parte de un repo de git y el archivo no está ignorado, se hace un commit **solo de ese archivo** (`<proyecto>: compose.yml editado desde Maestro`), con el autor de `--git-author`. Sin push. Si no hay repo o el archivo está en el `.gitignore` (lo normal para el `.env`), se guarda igual y se avisa.
- **Acciones**: `up` (`up -d`), `down` (pide confirmación), `restart` y `pull`, sobre el stack entero. La salida llega en vivo al mismo visor de los logs. Hay una operación por stack a la vez: otra acción o un guardado en el medio se rechazan.
- Una acción no se corta si se cierra la página: un `up` a medias deja el stack roto. El hub sigue leyendo la salida y la guarda una hora; al volver a la página en los 10 minutos siguientes, se ve.
- `docker compose` corre con el entorno del agent menos las variables `MAESTRO_*` (el token no queda al alcance de la interpolación).

## Informe para IA

Un resumen en Markdown de un servicio, para pegarlo en un agente de IA o para que un agente lo pida solo: qué es y dónde vive, el estado con sus motivos, cada contenedor (imagen, estado, health, exit code, arranque y reinicios), los checks de las últimas 24 horas (último resultado, disponibilidad, vencimiento del certificado y las últimas 10 fallas), los cambios de estado, las últimas 200 líneas de log de cada contenedor con los errores (`E`) y los avisos (`W`) marcados, y el `compose.yml` y el `.env`.

- En la página del servicio, el botón **Copiar informe** lo copia al portapapeles. Si el navegador no lo deja (sin HTTPS no da el portapapeles), lo muestra en un diálogo para copiarlo a mano.
- `GET /api/services/<id>/report` da el mismo texto. Pide sesión o el token de la API (`api_token` en `maestro.yaml`, de al menos 32 caracteres, en una variable); sin ninguno de los dos responde 401:

```sh
curl -H "Authorization: Bearer $MAESTRO_API_TOKEN" https://<maestro>/api/services/media-stack/jellyfin/report
```

**Los secretos se censuran siempre** (no hay modo sin censura: el informe está hecho para salir del servidor; el valor real se ve en la pestaña Compose, con sesión). Se reemplazan por `[censurado]`:

- en el `.env`, el valor de toda variable cuyo nombre parece de secreto: contiene `password`, `passwd`, `passphrase`, `secret`, `token`, `credential`, `private` o `apikey`, o tiene una palabra `pass`, `pwd`, `key`, `auth` o `salt` (`DB_PASS`, `homepage.widget.key`, `apiKey`). Los que terminan en `_FILE` son rutas y quedan. `true`, `false`, `yes`, `no`, `0` y `1` también quedan (`WEBUI_AUTH=true` no esconde nada);
- en el compose, lo mismo en `KEY: valor` y `- KEY=valor` (un bloque `|` entero), salvo que el valor sea solo una referencia (`${VAR}`, `${VAR:?mensaje}`), que no dice nada; y el valor por defecto de `${VAR:-defecto}` si la variable parece secreta o está censurada en el `.env`;
- en todo el informe (compose, logs, motivos, URLs de los checks): cada valor censurado del `.env` (de 4 caracteres o más) aparezca donde aparezca, la clave de `esquema://usuario:clave@` y el usuario de `esquema://token@`, los parámetros de query con nombre de secreto, los tokens de `Bearer` y `Basic` y las asignaciones `password=…`, `"token":"…"`.

La censura se equivoca a favor de tapar: `cache key: users:42` o `10-vnc-password.sh: executing` también quedan censurados. Se probó con los compose, los `.env` y las últimas 200 líneas de log de todos los stacks de un server real, sin que se escapara ningún valor.

## Configuración

Lo que se puede descubrir no se configura: todo contenedor aparece solo. Lo habitual se hace desde la interfaz (ver "Portada"); un servicio también se ajusta con labels en su `compose.yml` o con el archivo `maestro.yaml` del hub. Prioridad, de mayor a menor: `maestro.yaml`, la interfaz, labels `maestro.*`, labels `homepage.*` y nombres de compose. Los labels de Homepage ya existentes se aprovechan sin migrar nada.

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
| `maestro.ignore` | `true`: no mostrarlo ni vigilarlo | | se muestra |
| `maestro.separate` | `true`: tarjeta propia, fuera de la de su stack | | en la tarjeta del stack |

Una clave `maestro.*` desconocida o un valor inválido se ignora y queda en el log una vez. El check push no tiene label: su token es un secreto y va en `maestro.yaml`.

### `maestro.yaml`

Ver [`maestro.example.yaml`](maestro.example.yaml). Tiene estas secciones y una clave suelta:

- `api_token`: opcional, el token para pedir la API sin sesión (ver "Informe para IA").
- `hosts`: cada host con agent, con `agent` (URL) y `token`.
- `services`: con clave `<proyecto>/<servicio>` pisa valores de un servicio descubierto; con una clave sin `/` declara uno propio, que es una unidad systemd si tiene `unit` (y entonces necesita `host`) o un servicio externo si no.
- `stacks`: con clave `<host>/<proyecto>`, `name`, `group` y `hidden` de la tarjeta de un stack. El host tiene que estar en `hosts`.
- `groups`: lista con el orden de los grupos de la portada. "Otros" no va: siempre queda al final.

| Clave de un servicio | Tipo | Dónde vale |
|---|---|---|
| `name`, `group`, `icon`, `url`, `description` | texto | todos |
| `check` | `{http, expect, redirects, tcp, push, interval, timeout}` (ver abajo) | todos |
| `alert`, `ignore` | `true`/`false` | todos |
| `separate` | `true`/`false` | servicios de compose |
| `hidden` | `true`: no se muestra en la portada; `false`: no se oculta desde la interfaz | todos |
| `host` | nombre de un host de `hosts` | solo propios |
| `unit` | unidad systemd | solo propios |

`check` lleva al menos uno de `http` (URL), `tcp` (`host:puerto`) o `push` (token, de al menos 16 caracteres y distinto en cada servicio), y opcionalmente `expect` y `redirects` (de `http`) e `interval` y `timeout` (duraciones de Go, para todos los del servicio). Para un push, `interval` es cada cuánto tiene que llegar la llamada. Un `check` en el archivo reemplaza a los de los labels.

Una clave desconocida es un error al arrancar, igual que una variable `${VAR}` que no existe en el entorno. Los secretos van siempre en variables, nunca escritos en el archivo. Dentro de un mapa entre llaves (`{ ... }`), la variable va entre comillas: `token: "${MAESTRO_TOKEN_VM}"`.

## Despliegue

`deploy/compose.yml` es un ejemplo con los dos contenedores en el mismo host:

- `hub` en la red del proxy (por ejemplo, `caddy_net`), sin socket de Docker.
- `agent` con `network_mode: host`, el socket montado y el grupo `docker` del host por `group_add`, el socket de D-Bus del sistema (para systemd), la raíz del host en `/hostfs` de solo lectura (para el espacio de los discos) y la carpeta de los stacks montada **en la misma ruta que en el host**: los labels de compose dan la ruta del host, y `docker compose` le pasa al daemon las rutas de los bind mounts tal cual. Corre con el uid y el gid del dueño de esa carpeta (`user:`), para que lo que escribe y los commits queden como suyos.

Son dos imágenes del mismo `Dockerfile`: el hub (por defecto, `distroless/static`) y el agent (`--target agent`, Alpine con el CLI de Docker, el plugin de compose y git). Los dos corren como usuario sin privilegios, con el sistema de archivos de solo lectura y sin capacidades. Mientras no hay ningún usuario, la pantalla de login ofrece crear el primero (usuario, contraseña de 12 caracteres o más y repetirla) y entra con él; apenas existe, esa pantalla desaparece y `/setup` se niega. La creación es atómica: de dos pedidos a la vez, gana uno. Queda en el log con la IP. Hasta crearlo, cualquiera que llegue a la página puede hacerlo: conviene crearlo enseguida después del primer despliegue. También se puede crear, o cambiar la contraseña, por la terminal:

```sh
docker compose run --rm hub passwd --db /data/maestro.db --user <usuario>
```

### Agent en un host sin Docker (LXC, Proxmox)

El binario es estático: se copia al host y corre como servicio de systemd, sin Docker. [`deploy/maestro-agent.service`](deploy/maestro-agent.service) es la unidad, con las instrucciones arriba. Las opciones del agent:

| Opción | Por defecto | Qué hace |
|---|---|---|
| `--listen` | `127.0.0.1:9101` | dónde escucha |
| `--docker` | `true` | lee Docker (contenedores, logs, stats y stacks); `false` en un host sin Docker |
| `--stacks-dir`, `--git-author` | | stacks de compose (necesita Docker) |
| `--systemd` | `true` | servicios y timers por el bus del sistema; en un contenedor hay que montar `/run/dbus/system_bus_socket` |
| `--proxmox` | `false` | LXC y VMs con `pvesh` (en el host de Proxmox, como root) |
| `--smart` | `false` | SMART con `smartctl` (como root) |
| `--hostfs` | | en un contenedor, dónde está montada la raíz del host (`/:/hostfs:ro`): los sistemas de archivos se buscan ahí |
| `--proc`, `--sys` | `/proc`, `/sys` | de dónde lee el host. En un contenedor con `network_mode: host`, el `/proc` propio ya da la CPU, la memoria, los discos y la red del host |

El token va siempre en `MAESTRO_AGENT_TOKEN` (no en un flag: se vería en `ps`). En el hub, cada host es una entrada de `hosts:` con la URL y el token.

## Seguridad

- Login desde la primera versión. Las contraseñas se guardan como hashes argon2id. La sesión es un token aleatorio en una cookie `HttpOnly`, `Secure` y `SameSite=Lax`, y en la base solo queda su hash.
- Los POST de otro origen se rechazan con `http.CrossOriginProtection`.
- El agent no se publica y exige un token Bearer. El token se compara en tiempo constante y no se muestra en los logs.
- Montar el socket con `:ro` no hace que la API de Docker sea de solo lectura. El agent levanta y baja stacks y edita sus compose: quien controle el agent (o el hub, que le habla con el token) tiene el equivalente a root en ese host. Por eso el login, el agent sin publicar y el acceso solo detrás del proxy.
- El agent solo lee y escribe el `compose.yml` y el `.env` de un proyecto, y solo dentro de `--stacks-dir`: una ruta que, resueltos los symlinks, cae afuera se rechaza.
- El agent de un contenedor monta la raíz del host de solo lectura, para medir el espacio de los discos: lee lo que su usuario puede leer, nada más. Con el socket de Docker ya podía mucho más.
- El agent de un host de Proxmox corre como root (lo piden `pvesh` y `smartctl`) y solo hace lecturas: sin Docker no edita ni levanta nada.
- El `.env` viaja del agent al navegador para editarlo: lleva secretos, y por eso esa página solo existe con sesión iniciada.
- El informe para IA sale censurado siempre. Con el token de la API se puede pedir sin sesión: el token se compara en tiempo constante y uno equivocado queda en el log con la IP.
- Las líneas de log se insertan como texto, nunca como HTML: un contenedor no puede meter marcado en la página.
- `/push/<token>` no pide login: el token es la credencial. Se compara en tiempo constante contra todos los tokens, sin cortar en el primero, y la respuesta es la misma exista o no. El token viaja en la URL, así que queda en el log de acceso del proxy si está activado.
