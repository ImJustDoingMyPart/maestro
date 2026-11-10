package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
)

// Nombres lógicos de los archivos de un stack que se pueden editar. Son
// lógicos porque el compose puede llamarse compose.yml, compose.yaml o
// docker-compose.yml: el agent sabe cuál es.
const (
	FileCompose = "compose"
	FileEnv     = "env"
)

// StackFile es un archivo de un stack con su contenido.
type StackFile struct {
	Name    string `json:"name"`    // FileCompose o FileEnv
	Path    string `json:"path"`    // ruta en el host
	Exists  bool   `json:"exists"`  // el .env puede no existir
	Content string `json:"content"` // vacío si no existe
	// Hash identifica el contenido leído ("sha256:<hex>"; vacío si no
	// existe). Una escritura lo tiene que mandar de vuelta: si el archivo
	// cambió en el medio, no coincide y la escritura se rechaza.
	Hash string `json:"hash"`
}

// StackFiles es el cuerpo de GET /v1/stacks/{project}/files.
type StackFiles struct {
	Project string      `json:"project"`
	Dir     string      `json:"dir"` // carpeta del proyecto
	Files   []StackFile `json:"files"`
	// Busy dice si hay una acción corriendo sobre el stack.
	Busy bool `json:"busy"`
}

// WriteRequest es el cuerpo de PUT /v1/stacks/{project}/files/{name} y de
// POST /v1/stacks/{project}/files/{name}/check.
type WriteRequest struct {
	Content string `json:"content"`
	Hash    string `json:"hash"` // el Hash que se leyó; no hace falta en /check
}

// CheckResult es la respuesta de /check: el diff contra lo que hay en el
// disco, sin escribir nada. Diff vacío: no hay cambios.
type CheckResult struct {
	Diff string `json:"diff"`
}

// WriteResult es la respuesta de una escritura.
type WriteResult struct {
	Hash string `json:"hash"` // el del contenido nuevo
	Diff string `json:"diff"` // vacío si el contenido era el mismo
	// Commit es el hash corto del commit; vacío si no se commiteó.
	Commit string `json:"commit,omitempty"`
	// Notice explica por qué no hubo commit, si no lo hubo y era de
	// esperar que lo hubiera (la carpeta no es un repo, el archivo está
	// ignorado, git falló).
	Notice string `json:"notice,omitempty"`
}

// StackSource maneja los stacks de compose del host. Lo implementa
// internal/stacks; los tests pasan uno falso.
type StackSource interface {
	Files(ctx context.Context, project string) (StackFiles, error)
	Check(ctx context.Context, project, name string, content []byte) (CheckResult, error)
	Write(ctx context.Context, project, name string, content []byte, hash string) (WriteResult, error)
}

// Errores de StackSource que el handler traduce a un código HTTP.
var (
	ErrStackNotFound = errors.New("el stack no existe")                      // 404
	ErrBadFile       = errors.New("solo se editan el compose y el .env")     // 404
	ErrConflict      = errors.New("el archivo cambió desde que se leyó")     // 409
	ErrBusy          = errors.New("hay otra operación en curso en el stack") // 409
	ErrOutsideRoot   = errors.New("el archivo está fuera de --stacks-dir")   // 403
)

// InvalidError es un archivo que no pasa `docker compose config`. Output es
// lo que dijo compose.
type InvalidError struct {
	Output string
}

func (e *InvalidError) Error() string { return "docker compose config falló: " + e.Output }

// ErrorBody es el cuerpo de un error del agent. Output solo viene con un
// 422 (archivo inválido).
type ErrorBody struct {
	Error  string `json:"error"`
	Output string `json:"output,omitempty"`
}

// projectRe es la regla de compose para nombres de proyecto: minúsculas,
// dígitos, "-" y "_", empezando por letra o dígito. Además deja afuera
// "/" y "..".
var projectRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ValidProject dice si p es un nombre de proyecto de compose válido.
func ValidProject(p string) bool { return len(p) <= 128 && projectRe.MatchString(p) }

// maxFileSize acota lo que se acepta para escribir: un compose de 1 MiB ya
// es raro.
const maxFileSize = 1 << 20

func stackRoutes(mux *http.ServeMux, st StackSource, logErr func(msg string, args ...any)) {
	// project valida el nombre y escribe el 400 si no sirve.
	project := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		p := r.PathValue("project")
		if !ValidProject(p) {
			writeJSON(w, http.StatusBadRequest, ErrorBody{Error: "nombre de proyecto inválido"})
			return "", false
		}
		return p, true
	}
	readBody := func(w http.ResponseWriter, r *http.Request) (WriteRequest, bool) {
		var req WriteRequest
		r.Body = http.MaxBytesReader(w, r.Body, maxFileSize+4<<10) // el JSON escapa un poco más
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorBody{Error: "cuerpo inválido: " + err.Error()})
			return req, false
		}
		if len(req.Content) > maxFileSize {
			writeJSON(w, http.StatusRequestEntityTooLarge, ErrorBody{Error: "el archivo pasa de 1 MiB"})
			return req, false
		}
		return req, true
	}

	mux.HandleFunc("GET /v1/stacks/{project}/files", func(w http.ResponseWriter, r *http.Request) {
		p, ok := project(w, r)
		if !ok {
			return
		}
		files, err := st.Files(r.Context(), p)
		if err != nil {
			stackError(w, err, logErr, "leer stack", p)
			return
		}
		writeJSON(w, http.StatusOK, files)
	})
	mux.HandleFunc("POST /v1/stacks/{project}/files/{name}/check", func(w http.ResponseWriter, r *http.Request) {
		p, ok := project(w, r)
		if !ok {
			return
		}
		req, ok := readBody(w, r)
		if !ok {
			return
		}
		res, err := st.Check(r.Context(), p, r.PathValue("name"), []byte(req.Content))
		if err != nil {
			stackError(w, err, logErr, "validar archivo", p)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("PUT /v1/stacks/{project}/files/{name}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := project(w, r)
		if !ok {
			return
		}
		req, ok := readBody(w, r)
		if !ok {
			return
		}
		res, err := st.Write(r.Context(), p, r.PathValue("name"), []byte(req.Content), req.Hash)
		if err != nil {
			stackError(w, err, logErr, "escribir archivo", p)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
}

// stackError traduce un error de StackSource. Los conocidos dicen qué pasó;
// el resto queda en el log del agent y al hub le llega un mensaje genérico.
func stackError(w http.ResponseWriter, err error, logErr func(msg string, args ...any), what, project string) {
	var inv *InvalidError
	switch {
	case errors.As(err, &inv):
		writeJSON(w, http.StatusUnprocessableEntity, ErrorBody{Error: "docker compose config falló", Output: inv.Output})
	case errors.Is(err, ErrStackNotFound), errors.Is(err, ErrBadFile):
		writeJSON(w, http.StatusNotFound, ErrorBody{Error: err.Error()})
	case errors.Is(err, ErrConflict), errors.Is(err, ErrBusy):
		writeJSON(w, http.StatusConflict, ErrorBody{Error: err.Error()})
	case errors.Is(err, ErrOutsideRoot):
		writeJSON(w, http.StatusForbidden, ErrorBody{Error: err.Error()})
	default:
		logErr(what, "project", project, "err", err)
		writeJSON(w, http.StatusInternalServerError, ErrorBody{Error: what + ": error interno (ver el log del agent)"})
	}
}
