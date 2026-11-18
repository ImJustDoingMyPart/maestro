package hub

import (
	"net/http"
	"slices"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/icons"
)

// El muestrario (/muestrario) junta en una página cada pieza de la
// interfaz con datos inventados: botones, insignias, chips, estados y
// cajas, en claro y en oscuro. Sirve para ver de un vistazo si algo se
// salió de su familia (criterio en PLAN.md, "Coherencia visual") y para
// aprobar la estética de una sola vez. Es una página de la aplicación
// (web/src/app/pages/Muestrario.svelte) y usa sus componentes reales (la
// tarjeta de la portada, HwCard, StatePill): lo que se ve acá es lo que se
// ve en las páginas.

// muestra son los datos del muestrario.
type muestra struct {
	Cards  []apiCard // en la forma de /api/portada, la que dibuja la tarjeta
	Tiles  []hwCard
	States []State
	Rows   []hiddenRow
}

func muestraData(ic *icons.Resolver) pageData {
	// Tres tarjetas del demo: un stack degradado, una arriba y una caída.
	groups := []group{{Name: "Muestra", Services: []serviceCard{
		demoGroups[0].Services[0], demoGroups[1].Services[0], demoGroups[1].Services[2],
	}}}
	groups[0].Services = slices.Clone(groups[0].Services)
	if ic != nil {
		decorateGroups(ic, groups, nil)
	}
	var cards []apiCard
	for _, c := range groups[0].Services {
		cards = append(cards, cardJSON(c))
	}
	m := &muestra{
		Cards: cards,
		Tiles: []hwCard{
			{Icon: "memory", Title: "CPU", Value: "23", Unit: "%", HasBar: true, Pct: 23, Sub: "8 núcleos · carga 1,84"},
			{Icon: "memory_alt", Title: "Memoria", Value: "14,2", Unit: "GB", HasBar: true, Pct: 93, Warn: true, Sub: "de 15,3 GB"},
			{Icon: "hard_drive", Title: "sda", Aside: "SSD", Value: "38", Unit: "°C", Sub: "Samsung 870 EVO · 1 TB", State: StateUp, StateLabel: "Sano", StateTitle: "Autoevaluación SMART: PASSED"},
			{Icon: "dns", Title: "dns", Aside: "LXC 102", Value: "–", Sub: "1 núcleo · 1,0 GB", State: StateStopped, StateLabel: "Detenido"},
		},
		States: []State{StateUp, StateDegraded, StateDown, StateStopped, StateUnknown},
		Rows: []hiddenRow{
			{Name: "Homepage", Detail: "stack en vm"},
			{Name: "watchtower", Detail: "servicio de compose en vm", Locked: true},
		},
	}
	// Una hora fija para "Actualizado" del encabezado de la portada.
	updated := time.Date(2026, 11, 13, 21, 5, 48, 0, time.UTC)
	return pageData{Title: "Muestrario", ShowNav: true, Muestra: m, Updated: updated}
}

func (s *server) muestrarioPage(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, http.StatusOK, muestraData(s.icons))
}
