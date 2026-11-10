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
	State      State
	Reason     string
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

// buildGroups arma la portada: las tarjetas por grupo, sin las ocultas, y
// cuántas cosas que existen están ocultas.
//
// Un proyecto de compose con más de un servicio visible (y sin Separate)
// es una sola tarjeta. Toma nombre, grupo, descripción y link del servicio
// que tiene link, si hay uno solo; si no, el nombre del proyecto y el grupo
// de stackGroup. Su
// semáforo es el peor de sus servicios, con el motivo de ese servicio. Los
// grupos van en orden alfabético con "Otros" (sin grupo) al final, y las
// tarjetas, por nombre, en los dos casos sin distinguir mayúsculas.
//
// names son los nombres puestos desde la interfaz: los de servicios ya
// vienen aplicados (applyNames); acá se usan los de los stacks.
func buildGroups(services []catalog.Service, statusOf func(id string) status.Status, hidden map[string]time.Time, names map[string]string) (groups []group, hiddenCount int) {
	isHidden := func(key string) bool {
		_, ok := hidden[key]
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
		if svc.Source != catalog.SourceCompose || svc.Separate {
			cards = append(cards, singleCard(svc, statusOf(svc.ID)))
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
		if n := names[c.Key]; n != "" {
			c.Name = n
		}
		cards = append(cards, c)
	}

	slices.SortFunc(cards, func(a, b serviceCard) int {
		// "" (Otros) va al final.
		if (a.Group == "") != (b.Group == "") {
			if a.Group == "" {
				return 1
			}
			return -1
		}
		// Sin distinguir mayúsculas: "media" va antes que "Web".
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Group), strings.ToLower(b.Group)),
			cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			cmp.Compare(a.Key, b.Key))
	})
	for _, c := range cards {
		name := cmp.Or(c.Group, "Otros")
		if n := len(groups); n == 0 || groups[n-1].Name != name {
			groups = append(groups, group{Name: name})
		}
		g := &groups[len(groups)-1]
		g.Services = append(g.Services, c)
	}
	return groups, hiddenCount
}

func singleCard(svc catalog.Service, st status.Status) serviceCard {
	return serviceCard{
		Href:        servicePath(svc.ID),
		Name:        svc.Name,
		Description: describe(svc),
		Icon:        sourceIcon(svc.Source),
		URL:         svc.URL,
		State:       State(st.State),
		Reason:      st.Reason(),
		Group:       svc.Group,
		Key:         serviceKey(svc.ID),
		NameLocked:  svc.NameFromFile,
	}
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
// el del proyecto.
func stackGroup(project string, members []catalog.Service) string {
	for _, m := range members {
		if m.Group != project {
			return m.Group
		}
	}
	return members[0].Group
}

func stackCard(host, project string, members []catalog.Service, statusOf func(string) status.Status) serviceCard {
	c := serviceCard{
		Name:        project,
		Description: fmt.Sprintf("%d servicios", len(members)),
		Icon:        sourceIcon(catalog.SourceCompose),
		Group:       stackGroup(project, members),
		Key:         stackKey(host, project),
		StackHref:   stackPath(host, project),
		State:       StateUp,
	}
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
		c.Name, c.URL, c.Group = main.Name, main.URL, main.Group
		if main.Description != "" {
			c.Description = fmt.Sprintf("%s · %d servicios", main.Description, len(members))
		}
	}

	worst := -1
	for _, m := range members {
		st := statusOf(m.ID)
		c.Members = append(c.Members, memberRow{
			Href: servicePath(m.ID), Name: m.Name, Key: serviceKey(m.ID), NameLocked: m.NameFromFile,
			State: State(st.State), Reason: st.Reason(),
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
