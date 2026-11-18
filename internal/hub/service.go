package hub

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/icons"
	"github.com/ImJustDoingMyPart/maestro/internal/status"
)

// serviceDetail es lo que muestra la página de un servicio.
type serviceDetail struct {
	ID          string
	Name        string
	Description string
	Icon        string
	Logo        *icons.Ref
	URL         string
	State       State
	Reasons     []string // motivos del estado; vacío si está arriba
	Source      string   // de dónde sale, en palabras
	Host        string
	Containers  []containerRow
	StackURL    string // pestaña Compose; vacío si no es de compose
	LogsURL     string // vacío si el servicio no tiene logs que mostrar
	NoLogs      string // por qué no hay logs, si LogsURL está vacío
	ReportURL   string // informe para IA
	PageURL     string // la página (pestaña Actividad)
	MetricsURL  string // pestaña Métricas; vacío si no tiene contenedores
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
	svc, ok := s.lookup(r.Context(), r.PathValue("id"))
	if !ok {
		s.page(w, r, http.StatusNotFound, pageData{
			Title: "No existe",
		})
		return
	}
	d := detail(svc, s.mon.Status(svc.ID))
	d.Logo, d.Icon = logoFor(s.icons, serviceHint(svc).withOverride(s.chosenIcons(r.Context())[serviceKey(svc.ID)], svc.IconFromFile), d.Icon)
	d.Checks, d.Events = s.checkPanels(r, svc)
	if d.LogsURL != "" {
		d.Events = nil
	}
	s.page(w, r, http.StatusOK, pageData{
		Title: svc.Name, Svc: &d,
	})
}

// metricsPage atiende /metrics/{id...}: la página del servicio con la
// pestaña Métricas.
func (s *server) metricsPage(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.lookup(r.Context(), r.PathValue("id"))
	if !ok || len(svc.Containers) == 0 {
		s.page(w, r, http.StatusNotFound, pageData{
			Title: "No existe",
		})
		return
	}
	d := detail(svc, s.mon.Status(svc.ID))
	d.Logo, d.Icon = logoFor(s.icons, serviceHint(svc).withOverride(s.chosenIcons(r.Context())[serviceKey(svc.ID)], svc.IconFromFile), d.Icon)
	s.page(w, r, http.StatusOK, pageData{
		Title: svc.Name, Svc: &d, Tab: "metrics", Charts: serviceCharts(svc),
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
		ReportURL:   "/api/services/" + escapeID(svc.ID) + reportSuffix,
		PageURL:     servicePath(svc.ID),
	}
	switch svc.Source {
	case catalog.SourceCompose:
		d.Source = "compose · " + svc.Project
		d.StackURL = stackPath(svc.Host, svc.Project) + "?svc=" + url.QueryEscape(svc.ID)
	case catalog.SourceContainer:
		d.Source = "contenedor suelto"
	case catalog.SourceSystemd:
		d.Source = "systemd · " + svc.Unit
		if svc.Unit != "" && svc.Host != "" {
			d.LogsURL = "/logs/" + escapeID(svc.ID) // el journal de la unidad
		} else {
			d.NoLogs = "Sin unidad ni host: no hay journal que mostrar. Abajo, sus cambios de estado."
		}
	case catalog.SourceGuest:
		_, kind, vmid, _ := parseFound(svc.ID)
		d.Source = foundKindLabel(kind) + " " + vmid
		d.NoLogs = "Una LXC o VM de Proxmox no tiene logs acá: solo su estado. Abajo, sus cambios de estado."
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
		d.MetricsURL = metricsPath(svc.ID)
	}
	return d
}

// servicePath es la URL de la página de un servicio.
func servicePath(id string) string { return "/services/" + escapeID(id) }
func metricsPath(id string) string { return "/metrics/" + escapeID(id) }

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
