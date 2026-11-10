package agent

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Container es un contenedor tal como viaja del agent al hub.
type Container struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Image    string            `json:"image"`
	State    string            `json:"state"`  // "running", "exited", …
	Health   string            `json:"health"` // "healthy", "unhealthy", "starting" o ""
	Restarts int               `json:"restarts"`
	Labels   map[string]string `json:"labels"`
}

// ContainersResponse es el cuerpo de GET /v1/containers. Va envuelto en un
// objeto, no como lista suelta, para poder sumar campos sin romper al hub.
type ContainersResponse struct {
	Containers []Container `json:"containers"`
}

// ContainerSource da los contenedores del host. Lo implementa
// internal/docker; los tests pasan uno falso.
type ContainerSource interface {
	Containers(ctx context.Context) ([]Container, error)
}

// MinTokenLen es el largo mínimo del token. Con `openssl rand -hex 32` sale
// de 64.
const MinTokenLen = 32

// ErrWeakToken sale de NewHandler si el token es más corto que MinTokenLen.
var ErrWeakToken = errors.New("el token del agent tiene que tener al menos 32 caracteres (openssl rand -hex 32)")

// requestTimeout acota cuánto puede tardar Docker en contestar una petición.
const requestTimeout = 15 * time.Second

// NewHandler arma las rutas del agent. Todas exigen el token.
func NewHandler(src ContainerSource, token string, logger *slog.Logger) (http.Handler, error) {
	if len(token) < MinTokenLen {
		return nil, ErrWeakToken
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/containers", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()

		cs, err := src.Containers(ctx)
		if err != nil {
			logger.Error("listar contenedores", "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "no se pudo leer Docker"})
			return
		}
		if cs == nil {
			cs = []Container{} // "[]" y no "null" en el JSON
		}
		writeJSON(w, http.StatusOK, ContainersResponse{Containers: cs})
	})

	return requireToken(token, mux), nil
}

// requireToken exige "Authorization: Bearer <token>".
//
// Compara los hashes y no los tokens: ConstantTimeCompare tarda lo mismo
// para dos cadenas del mismo largo, pero sale enseguida si los largos
// difieren, y eso diría el largo del token. Los hashes miden siempre 32 bytes.
func requireToken(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		gotHash := sha256.Sum256([]byte(got))
		if !ok || subtle.ConstantTimeCompare(gotHash[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token inválido"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v) // si el cliente se fue, no hay a quién avisarle
}
