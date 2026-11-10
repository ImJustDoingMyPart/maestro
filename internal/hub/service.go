package hub

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/status"
)

// serviceDetail es lo que muestra la página de un servicio.
type serviceDetail struct {
	ID          string
	Name        string
	Description string
	Icon        string
	URL         string
	State       State
	Reasons     []string // motivos del estado; vacío si está arriba
	Source      string   // de dónde sale, en palabras
	Host        string
	Containers  []containerRow
	StackURL    string // pestaña Compose; vacío si no es de compose
	LogsURL     string // vacío si el servicio no tiene logs que mostrar
	NoLogs      string // por qué no hay logs, si LogsURL está vacío
	ChecksURL   string // fragmento con el historial de checks; vacío si no tiene
	Checks      []checkPanel
	// Events son los cambios de estado recientes, para un servicio sin logs
	// (la línea de tiempo de los que tienen logs va por el visor).
	Events []eventRow
}

type containerRow struct {
	Name     string
	Image    string
	State    string
	Health   string
	Restarts int
}

func (s *server) servicePage(w http.ResponseWriter, r *http.Request) {
	snap := s.poller.Snapshot()
	svc, ok := findService(snap, r.PathValue("id"))
	if !ok {
		s.pages.renderCode(w, s.logger, http.StatusNotFound, "service", pageData{
			Title: "No existe", Active: "services", ShowNav: true,
		})
		return
	}
	d := detail(svc, s.mon.Status(svc.ID))
	if len(svc.Checks) > 0 {
		d.ChecksURL = "/fragments/checks/" + escapeID(svc.ID)
	}
	d.Checks, d.Events = s.checkPanels(r, svc)
	if d.LogsURL != "" {
		d.Events = nil
	}
	s.pages.render(w, s.logger, "service", pageData{
		Title: svc.Name, Active: "services", ShowNav: true, Svc: &d,
	})
}

func detail(svc catalog.Service, st status.Status) serviceDetail {
	d := serviceDetail{
		ID:          svc.ID,
		Name:        svc.Name,
		Description: describe(svc),
		Icon:        sourceIcon(svc.Source),
		URL:         svc.URL,
		State:       State(st.State),
		Reasons:     st.Reasons,
		Host:        svc.Host,
	}
	switch svc.Source {
	case catalog.SourceCompose:
		d.Source = "compose · " + svc.Project
		d.StackURL = stackPath(svc.Host, svc.Project) + "?svc=" + url.QueryEscape(svc.ID)
	case catalog.SourceContainer:
		d.Source = "contenedor suelto"
	case catalog.SourceSystemd:
		d.Source = "systemd · " + svc.Unit
		d.NoLogs = "Los logs de unidades systemd (el journal) llegan con los agents de otros hosts."
	case catalog.SourceExternal:
		d.Source = "externo"
		d.NoLogs = "Un servicio externo no tiene logs: solo checks. Abajo, sus cambios de estado."
	}
	for _, c := range svc.Containers {
		d.Containers = append(d.Containers, containerRow{
			Name: c.Name, Image: shortImage(c.Image), State: c.State, Health: c.Health, Restarts: c.Restarts,
		})
	}
	if len(svc.Containers) > 0 {
		d.LogsURL = "/logs/" + escapeID(svc.ID)
	}
	return d
}

// servicePath es la URL de la página de un servicio.
func servicePath(id string) string { return "/services/" + escapeID(id) }

// escapeID escapa cada segmento del ID por separado: la "/" de un ID de
// compose queda como separador de la ruta y no como %2F, que algunos
// proxies rechazan o decodifican.
func escapeID(id string) string {
	parts := strings.Split(id, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
