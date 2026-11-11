package hub

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"time"
)

// State es el semáforo de un servicio en la portada.
type State string

// Los valores son los de status.State: se convierten con State(st.State).
const (
	StateUp       State = "up"
	StateDegraded State = "degraded"
	StateDown     State = "down"
	StateUnknown  State = "unknown"
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
	default:
		return "Sin datos"
	}
}

// serviceCard es lo que la portada necesita de un servicio. Cuando exista
// internal/catalog, se arma a partir de catalog.Service.
type serviceCard struct {
	Href        string // página del servicio; vacío en el demo
	Name        string
	Description string
	Icon        string // nombre de un ícono de Material Symbols
	URL         string
	State       State
	Reason      string // motivo principal del estado; vacío si está arriba
	Group       string // "" va a "Otros"
	// Key es la clave para ocultarla ("service:<id>" o "stack:<host>/<proyecto>");
	// vacía en el demo, que no oculta nada.
	Key string
	// Members son los servicios de una tarjeta de stack; vacío en la de un
	// servicio.
	Members   []memberRow
	StackHref string // pestaña Compose del stack
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
	Up, Degraded, Down, Unknown int
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
	HiddenCount  int         // cuántas tarjetas están ocultas
	FoundPending int         // cuántos descubiertos quedan en la bandeja
	Found        []foundRow  // página de Descubiertos
	Export       string      // página de exportar: el YAML
	Hidden       []hiddenRow // página de ocultos
	HostProblems []HostStatus
	Updated      time.Time // hora de la última vuelta del poller; cero si no hubo

	// Login
	Error    string
	Username string
	Setup    bool // no hay usuarios: el formulario crea el primero
}

// funcs son las funciones que pueden usar las plantillas.
var funcs = template.FuncMap{
	// separateArgs arma los datos del ítem "Sacar del stack" / "Volver al
	// stack" del menú.
	"separateArgs": func(key string, out, locked bool) map[string]any {
		return map[string]any{"Key": key, "Out": out, "Locked": locked}
	},
	"base": path.Base, // nombre de un archivo sin la carpeta
	// localTime escribe una hora como <time> que web/static/localtime.js
	// pasa a la zona del navegador. layout es "time" (15:04:05) o
	// "datetime" (02/01 15:04).
	"localTime": func(t time.Time, layout string) template.HTML {
		fallback := t.UTC().Format("15:04:05") + " UTC"
		if layout == "datetime" {
			fallback = t.UTC().Format("02/01 15:04") + " UTC"
		}
		return template.HTML(fmt.Sprintf(`<time datetime="%s" data-local="%s">%s</time>`,
			t.UTC().Format(time.RFC3339), template.HTMLEscapeString(layout), fallback))
	},
}

// pages tiene una plantilla compilada por página: cada una es layout.html y
// partials.html más el archivo de la página, que define el bloque "content". Se compilan por
// separado porque todas definen "content" y en un mismo conjunto chocarían.
type pages map[string]*template.Template

func parsePages(tfs fs.FS, names ...string) (pages, error) {
	p := make(pages, len(names))
	for _, name := range names {
		t, err := template.New(name).Funcs(funcs).ParseFS(tfs, "layout.html", "partials.html", name+".html")
		if err != nil {
			return nil, fmt.Errorf("plantilla %s: %w", name, err)
		}
		p[name] = t
	}
	return p, nil
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

// renderFragment ejecuta solo el bloque block de la página, sin el layout:
// es lo que htmx pide para reemplazar una parte.
func (p pages) renderFragment(w http.ResponseWriter, logger *slog.Logger, page, block string, data pageData) {
	p.renderFragmentData(w, logger, page, block, data)
}

// renderFragmentData es renderFragment para un bloque que no recibe
// pageData.
func (p pages) renderFragmentData(w http.ResponseWriter, logger *slog.Logger, page, block string, data any) {
	t, ok := p[page]
	if !ok {
		logger.Error("página inexistente", "page", page)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, block, data); err != nil {
		logger.Error("render", "page", page, "block", block, "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}
