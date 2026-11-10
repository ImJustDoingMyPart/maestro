# Una sola imagen para los dos modos: el comando decide si es hub o agent.
# Sin BuildKit a propósito: se arma también con el builder clásico.

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

# distroless/static: sin shell ni gestor de paquetes, solo certificados CA y
# un usuario nonroot (65532).
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/maestro /maestro
# /data ya existe y es de nonroot: un volumen nombrado montado ahí hereda
# ese dueño y la base se puede crear.
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
EXPOSE 8090
ENTRYPOINT ["/maestro"]
CMD ["serve", "--listen", "0.0.0.0:8090", "--config", "/config/maestro.yaml", "--db", "/data/maestro.db"]
