package hub

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/icons"
)

// State es el semáforo de un servicio en la portada.
type State string

// Los valores son los de status.State: se convierten con State(st.State).
const (
	StateUp       State = "up"
	StateDegraded State = "degraded"
	StateDown     State = "down"
	StateUnknown  State = "unknown"
	StateStopped  State = "stopped"
)

// Label es el texto que acompaña al color, para no depender solo de él.
func (s State) Label() string {
	switch s {
	case StateUp:
		return "Arriba"
	case StateDegraded:
		return "Degradado"
	case StateDown:
		return "Caído"
	case StateStopped:
		return "Detenido"
	default:
		return "Sin datos"
	}
}

// serviceCard es lo que la portada necesita de un servicio. Cuando exista
// internal/catalog, se arma a partir de catalog.Service.
type serviceCard struct {
	// UpHref es la acción "up" del stack si está bajado (docker compose
	// down): la tarjeta muestra Levantar en vez de Abrir. Vacío si no.
	UpHref      string
	Href        string // página del servicio; vacío en el demo
	Name        string
	Description string
	Icon        string // nombre de un ícono de Material Symbols, si no hay logo
	// Logo es el de la aplicación (internal/icons); nil usa Icon.
	Logo *icons.Ref
	icon iconHint // de dónde sale Logo
	// IconSet es el ícono elegido desde la interfaz ("" si no hay);
	// IconLocked: el ícono viene de maestro.yaml y no se cambia desde acá.
	IconSet    string
	IconLocked bool
	URL        string
	State      State
	Reason     string // motivo principal del estado; vacío si está arriba
	Group      string // "" va a "Otros"
	// Key es la clave para ocultarla ("service:<id>" o "stack:<host>/<proyecto>");
	// vacía en el demo, que no oculta nada.
	Key string
	// Members son los servicios de una tarjeta de stack; vacío en la de un
	// servicio.
	Members     []memberRow
	StackHref   string // pestaña Compose del stack (o del proyecto de un servicio suelto)
	MetricsHref string // pestaña Métricas; vacío si no es un contenedor
	// NameLocked y GroupLocked: el nombre o el grupo vienen de
	// maestro.yaml y la interfaz no los cambia.
	NameLocked  bool
	GroupLocked bool
	// Separated: un servicio de compose sacado de la tarjeta de su stack
	// (se puede devolver). SeparateLocked: eso viene del archivo.
	Separated      bool
	SeparateLocked bool
	// EditHref: un externo creado desde la interfaz se edita ahí.
	EditHref string
	// Found: viene de la bandeja de Descubiertos; "Ocultar" lo devuelve ahí.
	Found bool
	// HiddenLocked: maestro.yaml dice hidden: false; no se oculta desde acá.
	HiddenLocked bool
	// Alert: avisa por Telegram (en un stack, alguno de sus servicios).
	// AlertLocked: lo fija maestro.yaml (en un stack, para todos).
	Alert       bool
	AlertLocked bool
}

type group struct {
	Name     string
	Services []serviceCard
	Otros    bool // las tarjetas sin grupo: no se renombra, borra ni mueve
	Locked   bool // alguna tarjeta tiene el grupo del archivo: no se renombra ni borra
	Empty    bool // creado desde la interfaz, sin tarjetas: solo se ve en el modo edición
	// OrderLocked: maestro.yaml fija el orden de los grupos (groups:).
	OrderLocked bool
}

type summary struct {
	Up, Degraded, Down, Unknown, Stopped int
}

func summarize(groups []group) summary {
	var s summary
	for _, g := range groups {
		for _, svc := range g.Services {
			switch svc.State {
			case StateUp:
				s.Up++
			case StateDegraded:
				s.Degraded++
			case StateDown:
				s.Down++
			case StateStopped:
				s.Stopped++
			default:
				s.Unknown++
			}
		}
	}
	return s
}

// pageData es lo que recibe la plantilla layout y, a través de ella, el
// bloque "content" de cada página.
type pageData struct {
	Title   string
	Active  string // destino resaltado en la navegación
	ShowNav bool   // false en el login
	Demo    bool
	Live    bool // la grilla se refresca sola (no en el demo)

	Svc   *serviceDetail // página de un servicio; nil si no existe
	Ext   *externalForm  // alta o edición de un servicio externo
	Stack *stackPage     // pestaña Compose de un stack

	Groups       []group
	Summary      summary
	HiddenCount  int            // cuántas tarjetas están ocultas
	FoundPending int            // cuántos descubiertos quedan en la bandeja
	Found        []foundHost    // página de Descubiertos, por host
	FoundFailing int            // descubiertos caídos que no están en la portada
	Hardware     []hostHardware // página de Hardware
	HWHost       string         // el host que se muestra en Hardware
	Charts       []chartSpec    // pestaña Métricas de un servicio
	Tab          string         // pestaña de la página del servicio: "" (Actividad) o "metrics"
	Export       string         // página de exportar: el YAML
	Hidden       []hiddenRow    // página de ocultos
	HostProblems []HostStatus
	Updated      time.Time // hora de la última vuelta del poller; cero si no hubo
	OOB          bool      // fragmento de la portada: la hora del encabezado va con hx-swap-oob
	Muestra      *muestra  // página del muestrario

	// Login
	Error    string
	Username string
	Setup    bool // no hay usuarios: el formulario crea el primero
}

// pages tiene una plantilla compilada por página: layout.html más el
// archivo de la página (el login), o app.html sola (el shell de la
// aplicación, que define su propio "layout").
type pages map[string]*template.Template

func parsePages(tfs fs.FS, names ...string) (pages, error) {
	p := make(pages, len(names))
	for _, name := range names {
		files := []string{"layout.html", name + ".html"}
		t, err := template.New(name).ParseFS(tfs, files...)
		if err != nil {
			return nil, fmt.Errorf("plantilla %s: %w", name, err)
		}
		p[name] = t
	}
	return p, nil
}

// page responde una página de la aplicación (web/src/app). La misma URL
// sirve las dos cosas: con Accept: application/json, data en JSON (lo que
// pide el router de la aplicación al navegar); si no, el shell, que monta
// la aplicación y vuelve a pedir la URL como JSON. code va en las dos.
func (s *server) page(w http.ResponseWriter, r *http.Request, code int, data pageData) {
	w.Header().Add("Vary", "Accept")
	if wantsJSON(r) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, code, data)
		return
	}
	s.pages.renderCode(w, s.logger, code, "app", pageData{Title: data.Title, Demo: data.Demo})
}

// wantsJSON dice si el pedido es del router de la aplicación.
func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// render ejecuta primero en un buffer: si la plantilla falla a la mitad, el
// cliente recibe un 500 limpio en vez de media página.
func (p pages) render(w http.ResponseWriter, logger *slog.Logger, name string, data pageData) {
	p.renderCode(w, logger, http.StatusOK, name, data)
}

// renderCode es render con un código de estado distinto de 200.
func (p pages) renderCode(w http.ResponseWriter, logger *slog.Logger, code int, name string, data pageData) {
	t, ok := p[name]
	if !ok {
		logger.Error("página inexistente", "page", name)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		logger.Error("render", "page", name, "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = buf.WriteTo(w)
}
