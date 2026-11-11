// Package report arma en Markdown el informe para IA de un servicio: lo que
// es, dónde vive, su estado, sus checks, sus logs y su compose, para
// pegarlo en un agente o pedirlo con curl.
//
// Render solo da formato: recibe los datos ya juntados y censurados (eso lo
// hace el hub, con internal/redact) y no decide qué entra.
package report

import (
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/loglevel"
)

// Report son los datos de un informe, ya censurados. Un string vacío, un
// time.Time cero o un slice vacío son "no hay": la línea o la sección
// correspondiente no se escribe (salvo donde se dice otra cosa).
type Report struct {
	Generated time.Time // hora del informe

	ID          string
	Name        string
	Description string
	Group       string
	URL         string
	Source      string // de dónde sale, en palabras: "compose · media-stack", "externo"
	Host        string

	State      string    // en palabras: "Arriba", "Degradado", "Caído", "Sin datos"
	StateSince time.Time // desde cuándo está en ese estado
	Reasons    []string

	Containers []Container

	// WindowHours es la ventana del historial de checks y de cambios de
	// estado.
	WindowHours int
	Checks      []Check
	Events      []Event // del más viejo al más nuevo

	// Logs son las líneas, de la más vieja a la más nueva. LogsTail es
	// cuántas se pidieron por contenedor. LogsNote dice por qué no hay
	// logs (un servicio externo, el agent no responde); si no está vacío,
	// reemplaza a las líneas.
	Logs     []LogLine
	LogsTail int
	LogsNote string

	// Files son el compose y el .env, en ese orden. FilesNote dice por qué
	// no se pudieron leer; si Files y FilesNote están vacíos (un servicio
	// que no es de compose), no hay sección de archivos.
	Files     []File
	FilesNote string
}

// Container es una réplica del servicio.
type Container struct {
	Name      string
	Image     string
	State     string // el State de Docker: "running", "exited"
	Health    string // "healthy", "unhealthy", "starting" o ""
	ExitCode  *int   // el último exit code conocido; nil si no se sabe
	StartedAt time.Time
	Restarts  int
}

// Check es un check del servicio, con su historial en la ventana. Los
// textos vienen ya formateados por el hub.
type Check struct {
	Kind       string    // "HTTP", "TCP", "Push"
	Target     string    // URL o host:puerto; vacío en push (el token es secreto)
	Last       string    // último resultado: "ok · 200", "falla: timeout (10s)"; vacío si no corrió
	LastAt     time.Time // hora del último resultado
	Latency    string    // "12 ms", "1,2 s"; vacío si no aplica
	Uptime     string    // disponibilidad en la ventana: "99,8 %"; vacío sin datos
	CertExpiry time.Time // vencimiento del certificado (HTTPS)
	// Failures son las últimas fallas en la ventana, de la más nueva a la
	// más vieja.
	Failures []Failure
}

// Failure es un resultado fallido de un check.
type Failure struct {
	At     time.Time
	Detail string
}

// Event es un cambio de estado.
type Event struct {
	At       time.Time
	From, To string // en palabras: "Arriba", "Caído"
	Reason   string // puede estar vacío
}

// LogLine es una línea de log, sin códigos ANSI.
type LogLine struct {
	At        time.Time // cero si Docker no la dio
	Container string    // vacío si el servicio tiene un solo contenedor
	Level     loglevel.Level
	Text      string
}

// File es el compose o el .env de un stack.
type File struct {
	Name    string // "compose.yml", ".env"
	Path    string // ruta en el host
	Exists  bool   // el .env puede no existir
	Content string
	Lang    string // lenguaje del bloque de código: "yaml", "dotenv"
}

// Render escribe el informe en Markdown. Ver testdata/*.md y los tests
// para el formato exacto.
func Render(r Report) string {
	panic("sin implementar")
}
