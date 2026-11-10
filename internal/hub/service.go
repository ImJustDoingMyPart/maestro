package hub

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
)

// serviceDetail es lo que muestra la página de un servicio.
type serviceDetail struct {
	ID          string
	Name        string
	Description string
	Icon        string
	URL         string
	State       State
	Source      string // de dónde sale, en palabras
	Host        string
	Containers  []containerRow
	LogsURL     string // vacío si el servicio no tiene logs que mostrar
	NoLogs      string // por qué no hay logs, si LogsURL está vacío
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
	d := detail(svc, snap)
	s.pages.render(w, s.logger, "service", pageData{
		Title: svc.Name, Active: "services", ShowNav: true, Svc: &d,
	})
}

func detail(svc catalog.Service, snap Snapshot) serviceDetail {
	d := serviceDetail{
		ID:          svc.ID,
		Name:        svc.Name,
		Description: describe(svc),
		Icon:        sourceIcon(svc.Source),
		URL:         svc.URL,
		State:       serviceState(svc, snap),
		Host:        svc.Host,
	}
	switch svc.Source {
	case catalog.SourceCompose:
		d.Source = "compose · " + svc.Project
	case catalog.SourceContainer:
		d.Source = "contenedor suelto"
	case catalog.SourceSystemd:
		d.Source = "systemd · " + svc.Unit
		d.NoLogs = "Los logs de unidades systemd (el journal) llegan con los agents de otros hosts."
	case catalog.SourceExternal:
		d.Source = "externo"
		d.NoLogs = "Un servicio externo no tiene logs: solo checks."
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
