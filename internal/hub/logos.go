package hub

import (
	"regexp"
	"strings"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/icons"
)

// iconHint es lo que hace falta para darle un logo a una tarjeta o a una
// página: el ícono declarado y los nombres con que adivinarlo.
type iconHint struct {
	spec  string
	hints []string
}

// serviceHint arma las pistas de un servicio: su nombre, el nombre del
// servicio en compose, el proyecto, la imagen y la unidad.
func serviceHint(svc catalog.Service) iconHint {
	h := iconHint{spec: svc.Icon, hints: []string{svc.Name}}
	if svc.Project != "" {
		_, name, _ := strings.Cut(svc.ID, "/")
		h.hints = append(h.hints, name, svc.Project)
	}
	if len(svc.Containers) > 0 {
		h.hints = append(h.hints, icons.ImageHints(svc.Containers[0].Image)...)
	}
	if svc.Unit != "" {
		h.hints = append(h.hints, svc.Unit)
	}
	return h
}

// symbolName es un nombre que puede ser de Material Symbols ("shield",
// "vpn_lock"): un icon: del archivo de antes de los logos.
var symbolName = regexp.MustCompile(`^[a-z0-9_]{2,40}$`)

// logoFor resuelve el logo. Si no hay, devuelve nil y el ícono de Material
// Symbols: el declarado si tiene esa forma, o fallback.
func logoFor(r *icons.Resolver, h iconHint, fallback string) (*icons.Ref, string) {
	if ref, ok := r.Resolve(h.spec, h.hints...); ok {
		return &ref, fallback
	}
	if h.spec != "" && symbolName.MatchString(h.spec) {
		return nil, h.spec
	}
	if h.spec != "" {
		// Declarado pero no está (todavía no llegó el índice, o no existe):
		// se prueba igual con las pistas.
		if ref, ok := r.Resolve("", h.hints...); ok {
			return &ref, fallback
		}
	}
	return nil, fallback
}

// decorateGroups pone los logos de las tarjetas de la portada.
func decorateGroups(r *icons.Resolver, groups []group) {
	for gi := range groups {
		for ci := range groups[gi].Services {
			c := &groups[gi].Services[ci]
			h := c.icon
			if h.spec == "" && len(h.hints) == 0 {
				h.hints = []string{c.Name} // el demo
			}
			c.Logo, c.Icon = logoFor(r, h, c.Icon)
		}
	}
}
