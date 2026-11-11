// Package report arma en Markdown el informe para IA de un servicio: lo que
// es, dónde vive, su estado, sus checks, sus logs y su compose, para
// pegarlo en un agente o pedirlo con curl.
//
// Render solo da formato: recibe los datos ya juntados y censurados (eso lo
// hace el hub, con internal/redact) y no decide qué entra.
package report

import (
	"fmt"
	"strings"
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
	var blocks []string
	add := func(b string) { blocks = append(blocks, b) }

	add("# Informe de " + r.Name)
	add("Generado por Maestro el " + fmtTime(r.Generated) + ". Las horas están en UTC. Los secretos del compose, del `.env` y de los logs están reemplazados por `[censurado]`.")

	// Qué es
	add("## Qué es")
	var que []string
	field := func(label, v string, code bool) {
		if v == "" {
			return
		}
		if code {
			v = "`" + v + "`"
		}
		que = append(que, "- **"+label+"**: "+v)
	}
	field("ID", r.ID, true)
	field("Descripción", r.Description, false)
	field("Grupo", r.Group, false)
	field("URL", r.URL, false)
	field("Origen", r.Source, false)
	field("Host", r.Host, false)
	if len(que) > 0 {
		add(strings.Join(que, "\n"))
	}

	// Estado
	add("## Estado: " + r.State)
	if !r.StateSince.IsZero() {
		add("Desde " + fmtTime(r.StateSince) + ".")
	}
	if len(r.Reasons) > 0 {
		items := make([]string, len(r.Reasons))
		for i, reason := range r.Reasons {
			items[i] = "- " + reason
		}
		add(strings.Join(items, "\n"))
	}
	if len(r.Containers) > 0 {
		add("### Contenedores")
		rows := [][]string{{"Contenedor", "Imagen", "Estado", "Health", "Exit code", "Arrancó", "Reinicios"}}
		for _, c := range r.Containers {
			exit := ""
			if c.ExitCode != nil {
				exit = fmt.Sprint(*c.ExitCode)
			}
			started := ""
			if !c.StartedAt.IsZero() {
				started = fmtTime(c.StartedAt)
			}
			rows = append(rows, []string{c.Name, c.Image, c.State, c.Health, exit, started, fmt.Sprint(c.Restarts)})
		}
		add(table(rows))
	}

	// Checks
	add(fmt.Sprintf("## Checks (últimas %d h)", r.WindowHours))
	if len(r.Checks) == 0 {
		add("Sin checks declarados.")
	} else {
		rows := [][]string{{"Check", "Objetivo", "Último resultado", "Hora", "Latencia", "Disponibilidad", "Certificado vence"}}
		for _, c := range r.Checks {
			last := c.Last
			if last == "" {
				last = "todavía no corrió"
			}
			at, cert := "", ""
			if !c.LastAt.IsZero() {
				at = fmtTime(c.LastAt)
			}
			if !c.CertExpiry.IsZero() {
				cert = c.CertExpiry.UTC().Format("2006-01-02")
			}
			rows = append(rows, []string{c.Kind, c.Target, last, at, c.Latency, c.Uptime, cert})
		}
		add(table(rows))
		for _, c := range r.Checks {
			if len(c.Failures) == 0 {
				continue
			}
			add("### Últimas fallas de " + c.Kind)
			items := make([]string, len(c.Failures))
			for i, f := range c.Failures {
				items[i] = "- " + fmtTime(f.At) + " · " + f.Detail
			}
			add(strings.Join(items, "\n"))
		}
	}

	// Cambios de estado
	add(fmt.Sprintf("## Cambios de estado (últimas %d h)", r.WindowHours))
	if len(r.Events) == 0 {
		add("Ninguno.")
	} else {
		items := make([]string, len(r.Events))
		for i, e := range r.Events {
			items[i] = "- " + fmtTime(e.At) + " · " + e.From + " → " + e.To
			if e.Reason != "" {
				items[i] += " · " + e.Reason
			}
		}
		add(strings.Join(items, "\n"))
	}

	// Logs
	add("## Logs")
	if r.LogsNote != "" {
		add(r.LogsNote)
	} else {
		prefix := fmt.Sprintf("Últimas %d líneas de cada contenedor: ", r.LogsTail)
		if len(r.Logs) == 0 {
			add(prefix + "ninguna.")
		} else {
			var errs, warns int
			var sb strings.Builder
			for _, l := range r.Logs {
				switch l.Level {
				case loglevel.Error:
					errs++
					sb.WriteString("E ")
				case loglevel.Warn:
					warns++
					sb.WriteString("W ")
				default:
					sb.WriteString("  ")
				}
				if !l.At.IsZero() {
					sb.WriteString(fmtTime(l.At) + " ")
				}
				if l.Container != "" {
					sb.WriteString("[" + l.Container + "] ")
				}
				sb.WriteString(strings.ReplaceAll(l.Text, "\n", " ") + "\n")
			}
			count := "sin errores ni avisos"
			if errs > 0 || warns > 0 {
				count = plural(errs, "error", "errores") + " y " + plural(warns, "aviso", "avisos")
			}
			add(prefix + count + ". `E` marca los errores y `W` los avisos.")
			add(codeBlock("text", sb.String()))
		}
	}

	// Archivos
	if r.FilesNote != "" {
		add("## Archivos del stack")
		add(r.FilesNote)
	} else {
		for _, f := range r.Files {
			add("## " + f.Name)
			switch {
			case !f.Exists:
				add("`" + f.Path + "`: no existe.")
			case f.Content == "":
				add("`" + f.Path + "`: vacío.")
			default:
				add("`" + f.Path + "`")
				content := f.Content
				if !strings.HasSuffix(content, "\n") {
					content += "\n"
				}
				add(codeBlock(f.Lang, content))
			}
		}
	}

	return strings.Join(blocks, "\n\n") + "\n"
}

func fmtTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// cell prepara el texto de una celda: vacío es "—", "|" se escapa y los
// saltos de línea pasan a espacios.
func cell(s string) string {
	if s == "" {
		return "—"
	}
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}

// table arma una tabla Markdown; la primera fila es el encabezado.
func table(rows [][]string) string {
	var sb strings.Builder
	for i, row := range rows {
		cells := make([]string, len(row))
		for j, c := range row {
			if i == 0 {
				cells[j] = c
			} else {
				cells[j] = cell(c)
			}
		}
		sb.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		if i == 0 {
			sb.WriteString("|" + strings.Repeat("---|", len(row)) + "\n")
		}
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

// codeBlock envuelve content (que termina en "\n") en una cerca más larga
// que cualquier tira de backticks que tenga adentro.
func codeBlock(lang, content string) string {
	longest, run := 0, 0
	for _, c := range content {
		if c == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	return fence + lang + "\n" + content + fence
}
