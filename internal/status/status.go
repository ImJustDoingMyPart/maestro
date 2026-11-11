// Package status decide el estado de un servicio a partir de sus
// contenedores y sus checks, y si un cambio de estado es un evento que se
// guarda. Son funciones puras: no leen la hora ni guardan nada.
package status

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// State es el semáforo de un servicio.
type State string

const (
	Up       State = "up"
	Degraded State = "degraded"
	Down     State = "down"
	Unknown  State = "unknown"
	// Stopped es algo parado sin haber fallado: un servicio de systemd
	// inactivo, un timer desactivado, una LXC o VM apagada. No es una
	// falla, pero sí un estado conocido (se guarda como evento).
	Stopped State = "stopped"
)

// Container es lo que importa de un contenedor del servicio.
type Container struct {
	Name     string
	State    string // State de Docker: "running", "exited", "restarting", "paused", "created", "dead"
	Health   string // "healthy", "unhealthy", "starting" o "" sin healthcheck
	ExitCode int    // exit code de la última vez que terminó (0 mientras corre: Docker lo borra al arrancar)
	// RestartedAt es la última vez que el hub lo vio reiniciar (mismo
	// contenedor, arranque nuevo); cero si no lo vio.
	RestartedAt time.Time
	// RestartExit es el exit code de antes de ese reinicio, si el hub lo
	// vio (en el estado "restarting"); nil si no.
	RestartExit *int
}

// Check es el estado acumulado de un check del servicio.
type Check struct {
	Kind string // "http", "tcp" o "push"
	// Results es cuántos resultados hubo desde que el check corre. Cero es
	// "todavía no hay datos": el check no cuenta para nada.
	Results int
	// Failures son las fallas seguidas al final: 0 si el último resultado
	// fue bueno.
	Failures int
	// Detail es el detalle del último resultado ("timeout", "código 503").
	Detail string
	// CertExpiry es el vencimiento del certificado TLS que dio el último
	// resultado bueno de un check HTTPS; cero si no aplica.
	CertExpiry time.Time
}

// Unit es una unidad de systemd (un servicio o un timer) o una LXC o VM de
// Proxmox, tal como la reporta el agent de su host.
type Unit struct {
	Kind string // "service", "timer", "lxc" o "vm"
	Name string // nombre de la unidad, o de la LXC o VM
	// Missing: el agent responde pero no la reporta (se borró, o el nombre
	// del archivo está mal escrito).
	Missing bool
	// Active es el ActiveState de systemd (active, inactive, failed,
	// activating…) o, en una LXC o VM, su estado (running, stopped…).
	Active string
	// Result y ExitStatus son cómo terminó la última corrida: de la unidad
	// o, en un timer, del servicio que dispara. Result vacío: no se sabe.
	Result     string
	ExitStatus int
}

// Input es todo lo que se sabe de un servicio en un momento.
type Input struct {
	// Unit es la unidad o el invitado del que sale el servicio; nil si no
	// sale de uno. Como los contenedores, solo cuenta si HostOK.
	Unit *Unit
	// FromContainers es true si el servicio sale de Docker (compose o
	// contenedor suelto); false para systemd y externos, que por ahora solo
	// tienen checks.
	FromContainers bool
	// HostOK dice si el agent del host respondió en la última vuelta. Si es
	// false, los contenedores son datos viejos y no cuentan.
	HostOK     bool
	Containers []Container // en el orden en que se muestran
	Checks     []Check     // en el orden en que se muestran
}

// Params son los umbrales.
type Params struct {
	// FailThreshold es cuántas fallas seguidas de un check HTTP o TCP
	// tiran el servicio. Un push cae con una sola: su falla ya es "no llegó
	// a tiempo".
	FailThreshold int
	// RestartWindow es cuánto dura el degradado después de un reinicio.
	RestartWindow time.Duration
	// CertWarn es cuánto antes del vencimiento del certificado se degrada.
	CertWarn time.Duration
}

// DefaultParams son los umbrales de Maestro. Tres fallas seguidas es lo
// mismo que hace Uptime Kuma con tres reintentos.
var DefaultParams = Params{FailThreshold: 3, RestartWindow: 10 * time.Minute, CertWarn: 14 * 24 * time.Hour}

// Status es el estado de un servicio con sus motivos.
type Status struct {
	State State
	// Reasons son los motivos del estado, en castellano, el más importante
	// primero. Vacío si State es Up.
	Reasons []string
}

// Reason es el motivo principal; "" si no hay.
func (s Status) Reason() string {
	if len(s.Reasons) == 0 {
		return ""
	}
	return s.Reasons[0]
}

// Evaluate calcula el estado de un servicio en now.
//
// Down si hay al menos un motivo de caída; si no, Degraded si hay al menos
// uno de degradado; si no, Unknown si no hay datos o un contenedor está
// arrancando; si no, Up. Reasons tiene solo los motivos del estado que
// ganó, en este orden: primero los de los contenedores (en el orden de
// Input.Containers), después los de los checks (en el orden de
// Input.Checks). Los contenedores solo cuentan si FromContainers y HostOK.
//
// Motivos de caída (Down):
//   - contenedor que no está corriendo. State "exited": "<name> detenido
//     (exit <ExitCode>)"; "restarting": "<name> reiniciándose (exit
//     <ExitCode>)"; cualquier otro: "<name>: <State>".
//   - check http o tcp con Failures >= FailThreshold: "check <kind> falla:
//     <Detail>".
//   - check push con Failures >= 1: "push: <Detail>".
//
// Motivos de degradado (Degraded):
//   - contenedor corriendo con Health "unhealthy": "<name> unhealthy".
//   - contenedor corriendo con RestartedAt no cero y now - RestartedAt <
//     RestartWindow: "<name> reiniciado (exit <RestartExit>)", o "<name>
//     reiniciado" si RestartExit es nil. Un contenedor
//     puede dar este motivo y el de unhealthy, en ese orden: primero
//     unhealthy.
//   - check http o tcp con 0 < Failures < FailThreshold: "check <kind>
//     falla: <Detail>" (el mismo texto que la caída: el número de fallas no
//     va en el motivo, para que cada falla nueva no sea un evento).
//   - check con Failures == 0, CertExpiry no cero y CertExpiry - now <
//     CertWarn: "certificado vence el <AAAA-MM-DD>" (fecha de CertExpiry en
//     UTC). Si ya venció no hace falta caso aparte: el check falla.
//
// Unknown, sin motivos de caída ni de degradado:
//   - no hay datos: los contenedores y la unidad no cuentan (no hay, o
//     HostOK false) y ningún check tiene Results > 0. Motivo: "sin datos
//     del agent" si el servicio sale de contenedores o de una unidad y
//     !HostOK; "sin datos todavía" si no.
//   - FromContainers y HostOK, pero sin ningún contenedor: "sin
//     contenedores".
//   - un contenedor corriendo con Health "starting": "<name> arrancando"
//     (uno por contenedor en esa situación).
//
// Los motivos de Unit (ver unitReasons) van antes que los de los
// contenedores y los checks, si Unit no es nil y HostOK.
//
// Los checks con Results == 0 se ignoran del todo.
func Evaluate(in Input, now time.Time, p Params) Status {
	var down, degraded, unknown []string
	useContainers := in.FromContainers && in.HostOK
	var stopped []string
	useUnit := in.Unit != nil && in.HostOK
	if useUnit {
		d, g, st, u := unitReasons(*in.Unit)
		down, degraded, stopped, unknown = append(down, d...), append(degraded, g...), append(stopped, st...), append(unknown, u...)
	}

	if useContainers {
		for _, c := range in.Containers {
			if c.State != "running" {
				switch c.State {
				case "exited":
					down = append(down, fmt.Sprintf("%s detenido (exit %d)", c.Name, c.ExitCode))
				case "restarting":
					down = append(down, fmt.Sprintf("%s reiniciándose (exit %d)", c.Name, c.ExitCode))
				default:
					down = append(down, fmt.Sprintf("%s: %s", c.Name, c.State))
				}
				continue
			}
			if c.Health == "unhealthy" {
				degraded = append(degraded, c.Name+" unhealthy")
			}
			if !c.RestartedAt.IsZero() && now.Sub(c.RestartedAt) < p.RestartWindow {
				r := c.Name + " reiniciado"
				if c.RestartExit != nil {
					r += fmt.Sprintf(" (exit %d)", *c.RestartExit)
				}
				degraded = append(degraded, r)
			}
			if c.Health == "starting" {
				unknown = append(unknown, c.Name+" arrancando")
			}
		}
	}

	withData := false
	for _, ch := range in.Checks {
		if ch.Results == 0 {
			continue
		}
		withData = true
		switch {
		case ch.Kind == "push" && ch.Failures >= 1:
			down = append(down, "push: "+ch.Detail)
		case ch.Kind != "push" && ch.Failures >= p.FailThreshold:
			down = append(down, fmt.Sprintf("check %s falla: %s", ch.Kind, ch.Detail))
		case ch.Failures > 0:
			degraded = append(degraded, fmt.Sprintf("check %s falla: %s", ch.Kind, ch.Detail))
		case !ch.CertExpiry.IsZero() && ch.CertExpiry.Sub(now) < p.CertWarn:
			degraded = append(degraded, "certificado vence el "+ch.CertExpiry.UTC().Format("2006-01-02"))
		}
	}

	switch {
	case len(down) > 0:
		return Status{Down, down}
	case len(degraded) > 0:
		return Status{Degraded, degraded}
	case len(stopped) > 0:
		return Status{Stopped, stopped}
	case !useContainers && !useUnit && !withData:
		if in.FromContainers || in.Unit != nil {
			return Status{Unknown, []string{"sin datos del agent"}}
		}
		return Status{Unknown, []string{"sin datos todavía"}}
	case useContainers && len(in.Containers) == 0:
		return Status{Unknown, []string{"sin contenedores"}}
	case len(unknown) > 0:
		return Status{Unknown, unknown}
	}
	return Status{State: Up}
}

// unitReasons da los motivos de caída, de degradado, de detenido y de
// desconocido de una unidad o un invitado. Parar no es fallar: solo cae
// lo que falló.
//
//   - Missing: caída, "<name> no existe en el host".
//   - servicio: active o reloading está bien; failed es caída, "<name>
//     falló (exit <ExitStatus>)" si Result es "exit-code" y "<name> falló
//     (<Result>)" si no (o "<name> falló" sin Result); inactive es
//     detenido, "<name> detenido"; activating, "<name> arrancando"
//     (desconocido); deactivating, "<name> deteniéndose" (desconocido);
//     cualquier otro, desconocido "<name>: <Active>".
//   - timer: si Result no es "" ni "success", caída "última corrida falló
//     (exit <ExitStatus>)" o "última corrida falló (<Result>)". Si no, un
//     timer que no está active es detenido: "timer detenido".
//   - lxc o vm: running está bien; stopped es detenido, "<name> apagada";
//     cualquier otro estado, degradado "<name>: <Active>".
func unitReasons(u Unit) (down, degraded, stopped, unknown []string) {
	if u.Missing {
		return []string{u.Name + " no existe en el host"}, nil, nil, nil
	}
	failed := func(prefix string) string {
		switch u.Result {
		case "":
			return prefix
		case "exit-code":
			return fmt.Sprintf("%s (exit %d)", prefix, u.ExitStatus)
		}
		return prefix + " (" + u.Result + ")"
	}
	switch u.Kind {
	case "timer":
		if u.Result != "" && u.Result != "success" {
			down = append(down, failed("última corrida falló"))
		} else if u.Active != "active" {
			stopped = append(stopped, "timer detenido")
		}
	case "lxc", "vm":
		switch u.Active {
		case "running":
		case "stopped":
			stopped = append(stopped, u.Name+" apagada")
		default:
			degraded = append(degraded, u.Name+": "+u.Active)
		}
	default:
		switch u.Active {
		case "active", "reloading":
		case "failed":
			down = append(down, failed(u.Name+" falló"))
		case "inactive":
			stopped = append(stopped, u.Name+" detenido")
		case "activating":
			unknown = append(unknown, u.Name+" arrancando")
		case "deactivating":
			unknown = append(unknown, u.Name+" deteniéndose")
		default:
			unknown = append(unknown, u.Name+": "+u.Active)
		}
	}
	return down, degraded, stopped, unknown
}

// Event es un cambio de estado que se guarda.
type Event struct {
	From, To State
	Reason   string
}

// Change compara el último estado guardado (prev) con el nuevo (next) y dice
// si hay un evento que guardar.
//
// No hay evento si next es Unknown: no tener datos no es un cambio del
// servicio (el agent no respondió, el hub recién arranca). Tampoco si prev y
// next tienen el mismo State y el mismo Reason(). Hay evento en cualquier
// otro caso; en particular, de prev Unknown a un estado conocido sí es
// evento. Si nunca se guardó nada, prev es Status{} (State ""): cuenta
// como distinto de cualquier estado conocido, y From del evento queda "".
//
// Reason del evento: next.Reason() si next no es Up. Si next es Up:
// "volvió a responder" si algún motivo de prev empieza con "check " o con
// "push:"; si no, "arriba".
func Change(prev, next Status) (Event, bool) {
	if next.State == Unknown {
		return Event{}, false
	}
	if prev.State == next.State && prev.Reason() == next.Reason() {
		return Event{}, false
	}
	reason := next.Reason()
	if next.State == Up {
		reason = "arriba"
		// Si lo que lo tenía caído o degradado eran los checks, "volvió a
		// responder" dice más que "arriba".
		if slices.ContainsFunc(prev.Reasons, func(r string) bool {
			return strings.HasPrefix(r, "check ") || strings.HasPrefix(r, "push:")
		}) {
			reason = "volvió a responder"
		}
	}
	return Event{From: prev.State, To: next.State, Reason: reason}, true
}
