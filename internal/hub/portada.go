package hub

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/status"
)

// Claves de lo que se puede ocultar: un servicio o un stack entero.
func serviceKey(id string) string                { return "service:" + id }
func stackKey(host, project string) string       { return "stack:" + host + "/" + project }
func stackHideLabel(host, project string) string { return project + " (" + host + ")" }

// memberRow es un servicio dentro de la tarjeta de un stack.
type memberRow struct {
	Href       string
	Name       string
	Key        string // para ocultarlo o cambiarle el nombre
	NameLocked bool   // el nombre viene del archivo: no se cambia desde acá
	// SeparateLocked: separate viene del archivo, no se saca desde acá.
	SeparateLocked bool
	State          State
	Reason         string
}

// stateRank ordena los estados de peor a mejor, para el semáforo de un
// stack: el peor de sus servicios.
func stateRank(s status.State) int {
	switch s {
	case status.Down:
		return 3
	case status.Degraded:
		return 2
	case status.Unknown:
		return 1
	default:
		return 0
	}
}

// uiPrefs es la configuración de la portada hecha desde la interfaz. Las
// claves son las de ocultar: "service:<id>" o "stack:<host>/<proyecto>".
type uiPrefs struct {
	Hidden map[string]time.Time
	Names  map[string]string
	// Groups es el grupo de cada tarjeta ("" es "Otros"); una clave que no
	// está deja el de los labels.
	Groups map[string]string
	// GroupOrder son los grupos que conoce la interfaz, en orden. Pueden
	// estar vacíos.
	GroupOrder []string
	// CardOrder es la posición de las tarjetas ordenadas a mano dentro de
	// su grupo.
	CardOrder map[string]int
	// Separate saca (true) o devuelve (false) un servicio de la tarjeta de
	// su stack, salvo que el archivo diga otra cosa.
	Separate map[string]bool
	// Shown es lo descubierto que se pasó a la portada, por clave.
	Shown map[string]bool
}

// otros es el grupo de las tarjetas sin grupo.
const otros = "Otros"

// buildGroups arma la portada: las tarjetas por grupo, sin las ocultas, y
// cuántas cosas que existen están ocultas.
//
// Un proyecto de compose con más de un servicio visible (y sin Separate)
// es una sola tarjeta. Toma nombre, grupo, descripción y link del servicio
// que tiene link, si hay uno solo; si no, el nombre del proyecto y el grupo
// de stackGroup. Su semáforo es el peor de sus servicios, con el motivo de
// ese servicio.
//
// El grupo de una tarjeta es el de la interfaz (p.Groups) salvo que venga
// del archivo. Los grupos van primero en el orden de p.GroupOrder, después
// los demás en orden alfabético y "Otros" (sin grupo) al final; los de
// p.GroupOrder sin tarjetas salen vacíos (Empty), para el modo edición. Las
// tarjetas van primero en el orden de p.CardOrder y después por nombre.
// Los órdenes alfabéticos no distinguen mayúsculas.
//
// Los nombres de servicios de p.Names ya vienen aplicados (applyNames);
// acá se usan los de los stacks.
//
// De found (la bandeja de Descubiertos) salen como tarjeta solo los de
// p.Shown.
func buildGroups(services []catalog.Service, found []Discovered, statusOf func(id string) status.Status, p uiPrefs) (groups []group, hiddenCount int) {
	isHidden := func(key string) bool {
		_, ok := p.Hidden[key]
		if ok {
			hiddenCount++
		}
		return ok
	}

	type stackBucket struct {
		host, project string
		members       []catalog.Service
	}
	var (
		cards   []serviceCard
		buckets []*stackBucket
		byKey   = map[string]*stackBucket{}
	)
	for _, svc := range services {
		if isHidden(serviceKey(svc.ID)) {
			continue
		}
		if v, ok := p.Separate[serviceKey(svc.ID)]; ok && !svc.SeparateFromFile {
			svc.Separate = v
		}
		if svc.Source != catalog.SourceCompose || svc.Separate {
			c := singleCard(svc, statusOf(svc.ID))
			c.Separated = svc.Source == catalog.SourceCompose
			cards = append(cards, c)
			continue
		}
		k := stackKey(svc.Host, svc.Project)
		b := byKey[k]
		if b == nil {
			b = &stackBucket{host: svc.Host, project: svc.Project}
			byKey[k] = b
			buckets = append(buckets, b)
		}
		b.members = append(b.members, svc)
	}
	for _, b := range buckets {
		if isHidden(stackKey(b.host, b.project)) {
			continue
		}
		if len(b.members) == 1 {
			cards = append(cards, singleCard(b.members[0], statusOf(b.members[0].ID)))
			continue
		}
		c := stackCard(b.host, b.project, b.members, statusOf)
		if n := p.Names[c.Key]; n != "" {
			c.Name = n
		}
		cards = append(cards, c)
	}

	for _, d := range found {
		if p.Shown[d.Key()] {
			c := foundCard(d)
			if n := p.Names[c.Key]; n != "" {
				c.Name = n
			}
			cards = append(cards, c)
		}
	}

	byGroup := map[string]*group{}
	for _, c := range cards {
		if g, ok := p.Groups[c.Key]; ok && !c.GroupLocked {
			c.Group = g
		}
		name := cmp.Or(c.Group, otros)
		g := byGroup[name]
		if g == nil {
			g = &group{Name: name, Otros: name == otros}
			byGroup[name] = g
		}
		g.Locked = g.Locked || c.GroupLocked
		g.Services = append(g.Services, c)
	}
	for _, name := range p.GroupOrder {
		if byGroup[name] == nil && name != otros {
			byGroup[name] = &group{Name: name, Empty: true}
		}
	}

	pos := map[string]int{}
	for i, name := range p.GroupOrder {
		if _, ok := pos[name]; !ok {
			pos[name] = i
		}
	}
	for _, g := range byGroup {
		slices.SortFunc(g.Services, func(a, b serviceCard) int {
			pa, oka := p.CardOrder[a.Key]
			pb, okb := p.CardOrder[b.Key]
			switch {
			case oka && okb && pa != pb:
				return cmp.Compare(pa, pb)
			case oka && !okb:
				return -1
			case okb && !oka:
				return 1
			}
			return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.Key, b.Key))
		})
		groups = append(groups, *g)
	}
	slices.SortFunc(groups, func(a, b group) int {
		if a.Otros != b.Otros {
			if a.Otros {
				return 1
			}
			return -1
		}
		pa, oka := pos[a.Name]
		pb, okb := pos[b.Name]
		switch {
		case oka && okb:
			return cmp.Compare(pa, pb)
		case oka:
			return -1
		case okb:
			return 1
		}
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.Name, b.Name))
	})
	return groups, hiddenCount
}

func singleCard(svc catalog.Service, st status.Status) serviceCard {
	return serviceCard{
		Href:           servicePath(svc.ID),
		Name:           svc.Name,
		Description:    describe(svc),
		Icon:           sourceIcon(svc.Source),
		URL:            svc.URL,
		State:          State(st.State),
		Reason:         st.Reason(),
		Group:          svc.Group,
		Key:            serviceKey(svc.ID),
		NameLocked:     svc.NameFromFile,
		GroupLocked:    svc.GroupFromFile,
		SeparateLocked: svc.SeparateFromFile,
		EditHref:       editHref(svc),
	}
}

// editHref es la página de edición de un externo creado desde la interfaz.
func editHref(svc catalog.Service) string {
	if svc.FromUI {
		return "/externals/" + svc.ID
	}
	return ""
}

// applyNames devuelve una copia de services con los nombres puestos desde
// la interfaz, salvo donde el nombre viene del archivo (archivo > interfaz
// > labels).
func applyNames(services []catalog.Service, names map[string]string) []catalog.Service {
	if len(names) == 0 {
		return services
	}
	out := slices.Clone(services)
	for i := range out {
		if n := names[serviceKey(out[i].ID)]; n != "" && !out[i].NameFromFile {
			out[i].Name = n
		}
	}
	return out
}

// stackGroup es el grupo de un stack sin servicio principal: el primero que
// declare alguno de sus servicios (por label o archivo, o sea distinto del
// nombre del proyecto, que es el grupo por defecto); si ninguno declara,
// el del proyecto. locked dice que sale del archivo.
func stackGroup(project string, members []catalog.Service) (group string, locked bool) {
	for _, m := range members {
		if m.Group != project {
			return m.Group, m.GroupFromFile
		}
	}
	return members[0].Group, members[0].GroupFromFile
}

func stackCard(host, project string, members []catalog.Service, statusOf func(string) status.Status) serviceCard {
	c := serviceCard{
		Name:        project,
		Description: fmt.Sprintf("%d servicios", len(members)),
		Icon:        sourceIcon(catalog.SourceCompose),
		Key:         stackKey(host, project),
		StackHref:   stackPath(host, project),
		State:       StateUp,
	}
	c.Group, c.GroupLocked = stackGroup(project, members)
	var main *catalog.Service
	for i := range members {
		if members[i].URL == "" {
			continue
		}
		if main != nil { // más de uno con link: ninguno representa al stack
			main = nil
			break
		}
		main = &members[i]
	}
	if main != nil {
		c.Name, c.URL, c.Group, c.GroupLocked = main.Name, main.URL, main.Group, main.GroupFromFile
		if main.Description != "" {
			c.Description = fmt.Sprintf("%s · %d servicios", main.Description, len(members))
		}
	}

	worst := -1
	for _, m := range members {
		st := statusOf(m.ID)
		c.Members = append(c.Members, memberRow{
			Href: servicePath(m.ID), Name: m.Name, Key: serviceKey(m.ID), NameLocked: m.NameFromFile,
			SeparateLocked: m.SeparateFromFile,
			State:          State(st.State), Reason: st.Reason(),
		})
		if r := stateRank(st.State); r > worst {
			worst = r
			c.State = State(st.State)
			c.Reason = ""
			if st.Reason() != "" {
				c.Reason = m.Name + ": " + st.Reason()
			}
		}
	}
	slices.SortFunc(c.Members, func(a, b memberRow) int {
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return c
}

// foundKindLabel y kindIcon describen lo descubierto según su tipo.
func foundKindLabel(kind string) string {
	switch kind {
	case "unit":
		return "unidad de systemd"
	case "timer":
		return "timer de systemd"
	case "lxc":
		return "LXC"
	case "vm":
		return "VM"
	}
	return kind
}

func kindIcon(kind string) string {
	switch kind {
	case "timer":
		return "schedule"
	case "lxc":
		return "inventory_2"
	case "vm":
		return "computer"
	}
	return "settings"
}

// foundCard es la tarjeta de algo descubierto que se pasó a la portada.
func foundCard(d Discovered) serviceCard {
	return serviceCard{
		Name:        d.Name,
		Description: foundKindLabel(d.Kind) + " en " + d.Host,
		Icon:        kindIcon(d.Kind),
		State:       d.State,
		Key:         d.Key(),
		Found:       true,
	}
}
