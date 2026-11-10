package catalog

import (
	"log/slog"
)

// Container es lo que el catálogo necesita de un contenedor, tal como lo
// reporta el agent de un host.
type Container struct {
	Host     string // host del agent que lo reportó
	ID       string
	Name     string
	Image    string
	State    string // "running", "exited", … (el State de Docker)
	Health   string // "healthy", "unhealthy", "starting" o "" si no tiene healthcheck
	Restarts int
	Labels   map[string]string
}

// Source dice de dónde sale un servicio.
type Source string

const (
	SourceCompose   Source = "compose"   // contenedor con labels com.docker.compose.*
	SourceContainer Source = "container" // contenedor suelto, sin compose
	SourceSystemd   Source = "systemd"   // unidad declarada en maestro.yaml
	SourceExternal  Source = "external"  // servicio externo declarado en maestro.yaml
)

// CheckKind es el tipo de check. En la etapa 1 solo existe HTTP, y los checks
// se guardan pero no se ejecutan.
type CheckKind string

const CheckHTTP CheckKind = "http"

// Check es un chequeo declarado para un servicio.
type Check struct {
	Kind   CheckKind
	Target string // URL para HTTP
	Expect int    // código HTTP esperado; 0 si no se declaró
}

// FileService es una entrada de "services:" en maestro.yaml, ya leída por
// internal/config. Los punteros y los slices nil significan "no declarado":
// no pisan nada.
//
// La clave de la entrada decide qué es:
//   - con "/" ("media-stack/jellyfin"): pisa valores del servicio de compose
//     con ese ID. Nunca crea un servicio por sí sola; si el servicio no está
//     en Docker, la entrada no tiene efecto.
//   - sin "/" ("adguard"): es un servicio propio. Si tiene Unit, su fuente
//     es SourceSystemd; si no, SourceExternal. Si Name está vacío, el nombre
//     es la clave.
type FileService struct {
	Name        string
	Group       string
	Icon        string
	URL         string
	Description string
	Host        string  // solo para servicios propios
	Unit        string  // solo para servicios propios
	Checks      []Check // nil: se quedan los de los labels; no nil: los reemplaza
	Alert       *bool
	Ignore      *bool
}

// Service es un servicio ya combinado, listo para mostrar. Los slices sin
// elementos (ComposeFiles, Containers, Checks) son nil, no vacíos.
type Service struct {
	// ID identifica al servicio. Para compose es "<proyecto>/<servicio>";
	// para un contenedor suelto, su nombre; para uno declarado en el
	// archivo, su clave en maestro.yaml.
	ID          string
	Source      Source
	Name        string
	Group       string // "" si ninguna fuente lo da
	Icon        string // tal como viene del label o del archivo
	URL         string
	Description string
	Host        string

	Project      string   // proyecto de compose ("" si no es compose)
	ComposeFiles []string // de com.docker.compose.project.config_files, separado por comas
	Unit         string   // unidad systemd ("" si no es systemd)

	// Containers son las réplicas del servicio, ordenadas por Name. Vacío
	// para systemd y externos.
	Containers []Container

	Checks []Check
	// Alert dice si una caída avisa por Telegram. Si nadie lo declara, es
	// true cuando el servicio tiene checks.
	Alert bool
}

// Catalog combina las fuentes. Guarda qué claves de labels desconocidas ya
// registró, para avisar de cada una una sola vez en la vida del proceso
// aunque Build se llame cada 15 segundos.
type Catalog struct {
	logger *slog.Logger
	// El estado para "avisar una vez" va acá.
}

// New crea un catálogo que registra los avisos en logger.
func New(logger *slog.Logger) *Catalog {
	return &Catalog{logger: logger}
}

// Build combina contenedores y archivo y devuelve los servicios ordenados por
// Group, después Name, después ID.
//
// Prioridad de cada campo, de mayor a menor: archivo, labels maestro.*,
// labels homepage.*, nombres de compose. Las equivalencias son:
//
//	campo        maestro.*                homepage.*            compose
//	Name         maestro.name             homepage.name         com.docker.compose.service
//	Group        maestro.group            homepage.group        com.docker.compose.project
//	Icon         maestro.icon             homepage.icon
//	URL          maestro.url              homepage.href
//	Description  maestro.description      homepage.description
//	Checks       maestro.check.http (+ maestro.check.http.expect)
//	Alert        maestro.alert ("true"/"false", strconv.ParseBool)
//	Ignore       maestro.ignore (ídem)
//
// Un servicio ignorado (por label o por archivo) no aparece. Los contenedores
// con el mismo ID son réplicas de un solo servicio; sus labels se toman de la
// primera réplica por Name. Una clave maestro.* que no está en la tabla, o un
// valor inválido (alert "quizás", expect "abc"), se ignora y se registra con
// slog en nivel Warn una sola vez por clave. Las claves homepage.*
// desconocidas se ignoran sin avisar (Homepage tiene muchas, de widgets).
//
// Build puede llamarse desde varias goroutines a la vez.
func (c *Catalog) Build(containers []Container, file map[string]FileService) []Service {
	return nil
}
