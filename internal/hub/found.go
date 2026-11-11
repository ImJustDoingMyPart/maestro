package hub

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/status"
)

// Lo descubierto que se muestra en la portada pasa a ser un servicio del
// catálogo con ID igual a su clave ("found:<host>/<tipo>/<nombre>"): tiene
// estado, cambios de estado guardados y página, como cualquiera. La clave
// de su tarjeta es la misma (no "service:…"), así lo que ya se guardó en
// la bandeja (nombre, grupo) sigue valiendo.

// isFound dice si un ID de servicio es de algo descubierto.
func isFound(id string) bool { return strings.HasPrefix(id, "found:") }

// parseFound separa una clave "found:<host>/<tipo>/<nombre>". El nombre
// puede tener "/" (no debería, pero no se pierde).
func parseFound(key string) (host, kind, name string, ok bool) {
	rest, ok := strings.CutPrefix(key, "found:")
	if !ok {
		return "", "", "", false
	}
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// discoveredFrom arma lo descubierto de un host: sus servicios y timers de
// systemd y, si es un Proxmox, sus LXC y VMs. Las LXC y VMs se nombran por
// VMID, que no cambia; Label es su nombre.
func discoveredFrom(host string, sys agent.System, hostOK bool, now time.Time) []Discovered {
	var out []Discovered
	add := func(d Discovered, u status.Unit) {
		st := status.Evaluate(status.Input{Unit: &u, HostOK: hostOK}, now, status.DefaultParams)
		d.State, d.Reason = State(st.State), st.Reason()
		out = append(out, d)
	}
	for _, u := range sys.Units {
		kind, skind := "unit", "service"
		if strings.HasSuffix(u.Name, ".timer") {
			kind, skind = "timer", "timer"
		}
		d := Discovered{Host: host, Kind: kind, Name: u.Name, Label: u.Name, Detail: unitDetail(u, now)}
		add(d, unitState(skind, u))
	}
	for _, g := range sys.Guests {
		d := Discovered{Host: host, Kind: g.Kind, Name: strconv.Itoa(g.VMID), Label: g.Name, Detail: guestDetail(g)}
		add(d, status.Unit{Kind: g.Kind, Name: g.Name, Active: g.Status})
	}
	return out
}

func unitState(kind string, u agent.Unit) status.Unit {
	return status.Unit{Kind: kind, Name: u.Name, Active: u.Active, Result: u.Result, ExitStatus: u.ExitStatus}
}

// unitDetail es la línea de la bandeja: la descripción y, en un timer,
// cuándo corrió y cuándo corre.
func unitDetail(u agent.Unit, now time.Time) string {
	if !strings.HasSuffix(u.Name, ".timer") {
		return u.Description
	}
	var parts []string
	if !u.LastRun.IsZero() {
		parts = append(parts, "corrió hace "+ago(now.Sub(u.LastRun)))
	} else {
		parts = append(parts, "todavía no corrió")
	}
	if !u.NextRun.IsZero() && u.NextRun.After(now) {
		parts = append(parts, "próxima en "+ago(u.NextRun.Sub(now)))
	}
	return strings.Join(parts, " · ")
}

func guestDetail(g agent.Guest) string {
	kind := "VM"
	if g.Kind == "lxc" {
		kind = "LXC"
	}
	s := fmt.Sprintf("%s %d", kind, g.VMID)
	if g.MaxMem > 0 {
		s += " · " + humanBytes(float64(g.MaxMem)) + " de RAM"
	}
	if g.CPUs > 0 {
		s += " · " + plural(g.CPUs, "núcleo", "núcleos")
	}
	return s
}

// ago escribe una duración en palabras, redondeada a la unidad más grande.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "menos de un minuto"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minuto", "minutos")
	case d < 48*time.Hour:
		return plural(int(d/time.Hour), "hora", "horas")
	}
	return plural(int(d/(24*time.Hour)), "día", "días")
}

// humanBytes escribe bytes en múltiplos de 1024 con los nombres de
// siempre (KB, MB, GB) y coma decimal: "1,5 GB".
func humanBytes(b float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	if i == 0 || b >= 100 {
		return fmt.Sprintf("%.0f %s", b, units[i])
	}
	return strings.Replace(fmt.Sprintf("%.1f %s", b, units[i]), ".", ",", 1)
}

// foundService es el servicio del catálogo de algo descubierto que se
// muestra.
func foundService(d Discovered) catalog.Service {
	src := catalog.SourceSystemd
	if d.Kind == "lxc" || d.Kind == "vm" {
		src = catalog.SourceGuest
	}
	return catalog.Service{
		ID: d.Key(), Source: src, Name: d.Label, Host: d.Host, Unit: d.Name,
		Description: foundKindLabel(d.Kind) + " en " + d.Host,
	}
}

// unitFor busca la unidad o el invitado de un servicio en lo que reportó su
// host. nil si el servicio no sale de una unidad o si el agent no lee esa
// fuente (sin systemd, no es un Proxmox): eso es "sin datos", no "no
// existe".
func unitFor(svc catalog.Service, systems map[string]agent.System) *status.Unit {
	var host, kind, name string
	switch {
	case isFound(svc.ID):
		var ok bool
		if host, kind, name, ok = parseFound(svc.ID); !ok {
			return nil
		}
		if kind == "unit" {
			kind = "service"
		}
	case svc.Source == catalog.SourceSystemd && svc.Unit != "" && svc.Host != "":
		host, name, kind = svc.Host, svc.Unit, "service"
		if strings.HasSuffix(name, ".timer") {
			kind = "timer"
		} else if !strings.Contains(name, ".") {
			name += ".service" // systemctl acepta "nginx" por "nginx.service"
		}
	default:
		return nil
	}
	sys, ok := systems[host]
	if !ok {
		return nil
	}
	switch kind {
	case "lxc", "vm":
		if sys.Guests == nil {
			return nil
		}
		for _, g := range sys.Guests {
			if strconv.Itoa(g.VMID) == name && g.Kind == kind {
				return &status.Unit{Kind: kind, Name: g.Name, Active: g.Status}
			}
		}
	default:
		if sys.Units == nil {
			return nil
		}
		for _, u := range sys.Units {
			if u.Name == name {
				s := unitState(kind, u)
				return &s
			}
		}
	}
	return &status.Unit{Kind: kind, Name: name, Missing: true}
}

// withShown da los servicios de la foto más lo descubierto que se muestra
// y la foto todavía no tiene como servicio (el poller lo suma en la vuelta
// siguiente a "Mostrar"; una foto fija de los tests no lo suma nunca).
func withShown(snap Snapshot, shown map[string]bool) []catalog.Service {
	if len(shown) == 0 {
		return snap.Services
	}
	have := map[string]bool{}
	for _, svc := range snap.Services {
		if isFound(svc.ID) {
			have[svc.ID] = true
		}
	}
	out := snap.Services
	for _, d := range snap.Discovered {
		if shown[d.Key()] && !have[d.Key()] {
			out = append(slices.Clip(out), foundService(d))
		}
	}
	return out
}

// shownSetter es el poller, que vuelve a armar la foto cuando cambia lo
// que se muestra.
type shownSetter interface {
	SetShown(map[string]bool)
}
