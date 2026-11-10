# Dos imágenes desde el mismo build: el hub (por defecto) y el agent
# (--target agent). Sin BuildKit a propósito: se arma también con el builder
# clásico.
#
#   docker build -t maestro:dev .
#   docker build --target agent -t maestro-agent:dev .

FROM golang:1.27.3-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
# Sin cgo el binario es estático y corre sobre una imagen sin libc.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/maestro ./cmd/maestro \
    && mkdir -p /out/data

# Agent: corre docker compose y git, así que no le alcanza distroless. El
# CLI de Docker y el plugin de compose son binarios estáticos: se copian de
# la imagen oficial, con la versión del Docker del server. La base es la
# misma Alpine de esa imagen, más git (en Debian, git arrastra 100 MB).
FROM docker:29.9.3-cli AS dockercli

FROM alpine:3.24 AS agent
RUN apk add --no-cache git
COPY --from=dockercli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=dockercli /usr/local/libexec/docker/cli-plugins/docker-compose /usr/local/libexec/docker/cli-plugins/docker-compose
COPY --from=build /out/maestro /maestro
# Con read_only, HOME y la config de docker tienen que caer en un tmpfs.
ENV HOME=/tmp DOCKER_CONFIG=/tmp/.docker
# El agent tiene que correr con el uid del dueño de la carpeta de stacks
# (user: en el compose) para escribir sus archivos como él; por defecto,
# un uid sin privilegios.
USER 65532:65532
EXPOSE 9101
ENTRYPOINT ["/maestro"]
CMD ["agent", "--listen", "0.0.0.0:9101"]

# Hub: distroless/static, sin shell ni gestor de paquetes, solo certificados
# CA y un usuario nonroot (65532). Va último para ser la imagen por defecto.
FROM gcr.io/distroless/static-debian13:nonroot AS hub
COPY --from=build /out/maestro /maestro
# /data ya existe y es de nonroot: un volumen nombrado montado ahí hereda
# ese dueño y la base se puede crear.
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
EXPOSE 8090
ENTRYPOINT ["/maestro"]
CMD ["serve", "--listen", "0.0.0.0:8090", "--config", "/config/maestro.yaml", "--db", "/data/maestro.db"]
