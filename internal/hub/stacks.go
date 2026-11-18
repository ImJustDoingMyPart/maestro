package hub

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
)

// stackClient es lo que el hub necesita para los stacks. Lo cumple *Poller,
// que tiene un cliente por agent; los tests pasan uno falso.
type stackClient interface {
	StackFiles(ctx context.Context, host, project string) (agent.StackFiles, error)
	CheckStackFile(ctx context.Context, host, project, name, content string) (agent.CheckResult, error)
	WriteStackFile(ctx context.Context, host, project, name, content, hash string) (agent.WriteResult, error)
	StackAction(ctx context.Context, host, project, action string) (iter.Seq2[agent.ActionEvent, error], error)
}

// actionTimeout acota cuánto el hub sigue leyendo una acción. El agent
// corta a los 30 minutos; esto es un poco más.
const actionTimeout = 35 * time.Minute

// stackPage es lo que muestra la pestaña Compose.
type stackPage struct {
	Host     string
	Project  string
	Base     string // "/stacks/<host>/<proyecto>"
	Dir      string
	Compose  agent.StackFile
	Env      agent.StackFile
	Busy     bool
	Services []serviceLink // los servicios del stack que se conocen
	From     *serviceLink  // el servicio desde el que se llegó, para las pestañas
	Run      string        // URL del stream de la última acción, si es reciente
	RunName  string        // y qué acción es
	Err      string        // por qué no se pudo leer el stack
}

type serviceLink struct {
	Name string
	Href string
}

// stackPath es la URL de la pestaña Compose de un stack.
func stackPath(host, project string) string {
	return "/stacks/" + url.PathEscape(host) + "/" + url.PathEscape(project)
}

func (s *server) stackPage(w http.ResponseWriter, r *http.Request) {
	host, project := r.PathValue("host"), r.PathValue("project")
	if !agent.ValidProject(project) {
		http.Error(w, "proyecto inválido", http.StatusBadRequest)
		return
	}
	snap := s.poller.Snapshot()
	sp := stackPage{Host: host, Project: project, Base: stackPath(host, project)}
	for _, svc := range snap.Services {
		if svc.Source == catalog.SourceCompose && svc.Host == host && svc.Project == project {
			sp.Services = append(sp.Services, serviceLink{Name: svc.Name, Href: servicePath(svc.ID)})
		}
	}
	if id := r.URL.Query().Get("svc"); id != "" {
		if svc, ok := findService(snap, id); ok && svc.Host == host && svc.Project == project {
			sp.From = &serviceLink{Name: svc.Name, Href: servicePath(svc.ID)}
		}
	}
	if run, ok := s.runs.latest(host, project); ok {
		sp.Run, sp.RunName = "/runs/"+run.ID, run.Action
	}

	code := http.StatusOK
	files, err := s.stacks.StackFiles(r.Context(), host, project)
	switch {
	case err == nil:
		sp.Dir, sp.Busy = files.Dir, files.Busy
		for _, f := range files.Files {
			switch f.Name {
			case agent.FileCompose:
				sp.Compose = f
			case agent.FileEnv:
				sp.Env = f
			}
		}
	case errors.Is(err, errNoAgent):
		code, sp.Err = http.StatusNotFound, "No hay un agent configurado para el host "+host+"."
	case errors.Is(err, agent.ErrStackNotFound):
		code, sp.Err = http.StatusNotFound, "El agent no encuentra el stack "+project+": ni contenedores con ese proyecto ni una carpeta con un compose.yml."
	default:
		s.logger.Warn("leer stack", "host", host, "project", project, "err", err)
		code, sp.Err = http.StatusBadGateway, "No se pudo leer el stack: "+err.Error()
	}

	title := project
	if sp.From != nil {
		title = sp.From.Name
	}
	s.page(w, r, code, pageData{Title: title + " · Compose", Active: "services", ShowNav: true, Stack: &sp})
}

// fileRequest es el cuerpo JSON de validar y guardar.
type fileRequest struct {
	Content string `json:"content"`
	Hash    string `json:"hash"`
}

// readFileRequest valida la ruta y lee el cuerpo. Escribe el error si algo
// no sirve.
func readFileRequest(w http.ResponseWriter, r *http.Request) (host, project, name string, req fileRequest, ok bool) {
	host, project, name = r.PathValue("host"), r.PathValue("project"), r.PathValue("name")
	if !agent.ValidProject(project) || (name != agent.FileCompose && name != agent.FileEnv) {
		jsonError(w, http.StatusNotFound, "archivo inexistente", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "cuerpo inválido", "")
		return
	}
	return host, project, name, req, true
}

func (s *server) checkStackFile(w http.ResponseWriter, r *http.Request) {
	host, project, name, req, ok := readFileRequest(w, r)
	if !ok {
		return
	}
	res, err := s.stacks.CheckStackFile(r.Context(), host, project, name, req.Content)
	if err != nil {
		s.stackError(w, err, host, project)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) saveStackFile(w http.ResponseWriter, r *http.Request) {
	host, project, name, req, ok := readFileRequest(w, r)
	if !ok {
		return
	}
	res, err := s.stacks.WriteStackFile(r.Context(), host, project, name, req.Content, req.Hash)
	if err != nil {
		s.stackError(w, err, host, project)
		return
	}
	s.logger.Info("archivo de stack guardado", "host", host, "project", project, "file", name, "commit", res.Commit)
	writeJSON(w, http.StatusOK, res)
}

// stackError contesta un error del agent en JSON, con el mensaje que ve el
// usuario.
func (s *server) stackError(w http.ResponseWriter, err error, host, project string) {
	var inv *agent.InvalidError
	switch {
	case errors.As(err, &inv):
		jsonError(w, http.StatusUnprocessableEntity, "docker compose config rechazó el archivo: no se guardó.", inv.Output)
	case errors.Is(err, agent.ErrConflict):
		jsonError(w, http.StatusConflict, "El archivo cambió en el servidor desde que abriste la página. Recargala para ver la versión nueva (lo que editaste se pierde: copialo antes).", "")
	case errors.Is(err, agent.ErrBusy):
		jsonError(w, http.StatusConflict, "Hay otra operación corriendo en este stack. Esperá a que termine.", "")
	case errors.Is(err, errNoAgent), errors.Is(err, agent.ErrStackNotFound), errors.Is(err, agent.ErrBadFile):
		jsonError(w, http.StatusNotFound, err.Error(), "")
	default:
		s.logger.Warn("stack", "host", host, "project", project, "err", err)
		jsonError(w, http.StatusBadGateway, "Error hablando con el agent: "+err.Error(), "")
	}
}

// startAction arranca una acción de compose. El hub lee su salida hasta el
// final por su cuenta y contesta enseguida con la URL del stream, que el
// navegador abre con EventSource.
func (s *server) startAction(w http.ResponseWriter, r *http.Request) {
	host, project, action := r.PathValue("host"), r.PathValue("project"), r.PathValue("action")
	if !agent.ValidProject(project) || !agent.ValidAction(action) {
		jsonError(w, http.StatusNotFound, "acción inexistente", "")
		return
	}
	// Ni el pedido ni su contexto viven lo que dura la acción.
	ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
	events, err := s.stacks.StackAction(ctx, host, project, action)
	if err != nil {
		cancel()
		s.stackError(w, err, host, project)
		return
	}
	run := newRun(host, project, action, time.Now())
	s.runs.add(run)
	s.logger.Info("acción de stack", "host", host, "project", project, "action", action, "run", run.ID)

	go func() {
		defer cancel()
		defer func() {
			if p := recover(); p != nil {
				s.logger.Error("pánico leyendo una acción", "panic", p)
				run.finish(-1, "error interno del hub", time.Now())
			}
		}()
		for ev, err := range events {
			if err != nil {
				run.finish(-1, "se cortó la conexión con el agent; el comando puede haber seguido (ver el log del agent)", time.Now())
				return
			}
			if ev.Exit != nil {
				run.finish(*ev.Exit, ev.Error, time.Now())
				return
			}
			run.append(runLine{TS: ev.TS, Stream: ev.Stream, Line: ev.Line})
		}
	}()
	writeJSON(w, http.StatusOK, map[string]string{"run": "/runs/" + run.ID})
}

// streamRun atiende GET /runs/{id}: la salida de una acción como
// Server-Sent Events, desde el principio. El id de cada evento es su número
// de renglón: al reconectar, el navegador manda Last-Event-ID y sigue desde
// el siguiente. Al terminar manda "end" con {text, ok}.
func (s *server) streamRun(w http.ResponseWriter, r *http.Request) {
	run, ok := s.runs.get(r.PathValue("id"))
	if !ok {
		http.Error(w, "esa acción no existe o ya se borró", http.StatusNotFound)
		return
	}
	next := 0
	if id, err := strconv.Atoi(r.Header.Get("Last-Event-ID")); err == nil && id > 0 {
		next = id
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	sse := &sseWriter{w: w, rc: http.NewResponseController(w)}
	sse.raw("retry: 3000\n\n")

	ticker := time.NewTicker(keepalive)
	defer ticker.Stop()
	for {
		lines, done, wake := run.since(next)
		for _, l := range lines {
			next++
			data, _ := json.Marshal(l)
			sse.raw("id: " + strconv.Itoa(next) + "\ndata: " + string(data) + "\n\n")
		}
		if done {
			text, ok := run.summary()
			data, _ := json.Marshal(map[string]any{"text": text, "ok": ok})
			sse.raw("event: end\ndata: " + string(data) + "\n\n")
			_ = sse.flush()
			return
		}
		if sse.flush() != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-wake:
		case <-ticker.C:
			sse.comment("keepalive")
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, code int, msg, output string) {
	writeJSON(w, code, agent.ErrorBody{Error: msg, Output: output})
}
