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
)

// Container es lo que importa de un contenedor del servicio.
type Container struct {
	Name     string
	State    string // State de Docker: "running", "exited", "restarting", "paused", "created", "dead"
	Health   string // "healthy", "unhealthy", "starting" o "" sin healthcheck
	ExitCode int    // exit code de la última vez que terminó
	// RestartedAt es la última vez que el hub lo vio reiniciar (mismo
	// contenedor, arranque nuevo); cero si no lo vio.
	RestartedAt time.Time
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

// Input es todo lo que se sabe de un servicio en un momento.
type Input struct {
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
//     RestartWindow: "<name> reiniciado (exit <ExitCode>)". Un contenedor
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
//   - no hay datos: los contenedores no cuentan (FromContainers false o
//     HostOK false) y ningún check tiene Results > 0. Motivo: "sin datos
//     del agent" si FromContainers y !HostOK; "sin datos todavía" si no.
//   - FromContainers y HostOK, pero sin ningún contenedor: "sin
//     contenedores".
//   - un contenedor corriendo con Health "starting": "<name> arrancando"
//     (uno por contenedor en esa situación).
//
// Los checks con Results == 0 se ignoran del todo.
func Evaluate(in Input, now time.Time, p Params) Status {
	var down, degraded, unknown []string
	useContainers := in.FromContainers && in.HostOK

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
				degraded = append(degraded, fmt.Sprintf("%s reiniciado (exit %d)", c.Name, c.ExitCode))
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
	case !useContainers && !withData:
		if in.FromContainers {
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
