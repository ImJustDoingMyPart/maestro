package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// LogLine es una línea de log de un contenedor. En el cable va como NDJSON:
// un objeto JSON por línea.
type LogLine struct {
	TS     time.Time `json:"ts"`     // la marca de Docker; cero si no vino
	Stream string    `json:"stream"` // "stdout" o "stderr"
	Line   string    `json:"line"`
}

// LogOptions es lo que se le pide a Docker.
type LogOptions struct {
	Tail   int       // últimas N líneas; 0 = ninguna (con Follow, solo las nuevas)
	Since  time.Time // si no es cero, solo desde ese momento (para reconectar)
	Follow bool      // seguir esperando líneas nuevas hasta que se corte
}

const (
	// DefaultLogTail es el tail cuando el pedido no dice.
	DefaultLogTail = 200
	// MaxLogTail acota el tail: nadie pide el historial entero.
	MaxLogTail = 5000
)

// ErrContainerNotFound sale de ContainerSource.Logs si el contenedor no
// existe. El handler lo convierte en un 404.
var ErrContainerNotFound = errors.New("el contenedor no existe")

// parseLogOptions lee ?tail=&since=&follow= de un pedido. Un tail mayor que
// MaxLogTail se recorta en vez de rechazarse.
func parseLogOptions(q url.Values) (LogOptions, error) {
	opts := LogOptions{Tail: DefaultLogTail}
	if s := q.Get("tail"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return opts, fmt.Errorf("tail inválido: %q", s)
		}
		opts.Tail = min(n, MaxLogTail)
	}
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return opts, fmt.Errorf("since inválido (RFC 3339): %q", s)
		}
		opts.Since = t
	}
	if s := q.Get("follow"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return opts, fmt.Errorf("follow inválido: %q", s)
		}
		opts.Follow = b
	}
	return opts, nil
}

// encode es la inversa de parseLogOptions, para el cliente.
func (o LogOptions) encode() url.Values {
	q := url.Values{"tail": {strconv.Itoa(o.Tail)}}
	if !o.Since.IsZero() {
		q.Set("since", o.Since.Format(time.RFC3339Nano))
	}
	if o.Follow {
		q.Set("follow", "1")
	}
	return q
}

// handleLogs atiende GET /v1/containers/{id}/logs.
//
// Los errores de antes de empezar (contenedor inexistente, Docker caído)
// salen con su código HTTP. Una vez mandado el 200, un error solo puede
// cortar el stream: el hub lo ve como un fin antes de tiempo.
func handleLogs(src ContainerSource, logErr func(msg string, args ...any)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts, err := parseLogOptions(r.URL.Query())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		// Sin timeout: con follow, el pedido dura lo que el hub quiera. El
		// contexto del pedido se cancela cuando el hub corta, y con él la
		// lectura de Docker.
		ctx := r.Context()
		id := r.PathValue("id")
		lines, err := src.Logs(ctx, id, opts)
		if errors.Is(err, ErrContainerNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "contenedor inexistente"})
			return
		}
		if err != nil {
			logErr("abrir logs", "container", id, "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "no se pudo leer Docker"})
			return
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		rc := http.NewResponseController(w)
		_ = rc.Flush() // el hub sabe enseguida que el stream está abierto

		enc := json.NewEncoder(w)
		for line, err := range lines {
			if err != nil {
				if ctx.Err() == nil {
					logErr("leer logs", "container", id, "err", err)
				}
				return
			}
			// Encode y Flush fallan si el hub se fue; salir del range
			// corta el iterador, que cierra la lectura de Docker.
			if enc.Encode(line) != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
