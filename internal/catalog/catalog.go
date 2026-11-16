package catalog

import (
	"cmp"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Container es lo que el catálogo necesita de un contenedor, tal como lo
// reporta el agent de un host.
type Container struct {
	Host      string // host del agent que lo reportó
	ID        string
	Name      string
	Image     string
	State     string // "running", "exited", … (el State de Docker)
	Health    string // "healthy", "unhealthy", "starting" o "" si no tiene healthcheck
	Restarts  int
	ExitCode  int       // exit code de la última vez que terminó (0 si nunca terminó)
	StartedAt time.Time // última vez que arrancó; cero si nunca
	// LastExitCode y LastExitAt: la última vez que el agent lo vio terminar
	// (nil y cero si no lo vio). Sobreviven al rearranque, a diferencia de
	// ExitCode.
	LastExitCode *int
	LastExitAt   time.Time
	Labels       map[string]string
}

// Source dice de dónde sale un servicio.
type Source string

const (
	SourceCompose   Source = "compose"   // contenedor con labels com.docker.compose.*
	SourceContainer Source = "container" // contenedor suelto, sin compose
	SourceSystemd   Source = "systemd"   // unidad declarada en maestro.yaml
	SourceExternal  Source = "external"  // servicio externo declarado en maestro.yaml
	SourceGuest     Source = "guest"     // LXC o VM de Proxmox (descubierta y mostrada)
)

// CheckKind es el tipo de check. Un servicio tiene como mucho uno de cada
// tipo: el tipo identifica al check dentro del servicio.
type CheckKind string

const (
	CheckHTTP CheckKind = "http" // GET a una URL
	CheckTCP  CheckKind = "tcp"  // conectar a host:puerto y cerrar
	CheckDNS  CheckKind = "dns"  // preguntar un nombre a un servidor DNS
	// CheckPush lo hace un script llamando a /push/<token>; falla si la
	// llamada no llega a tiempo. Solo se declara en el archivo: el token
	// es un secreto.
	CheckPush CheckKind = "push"
)

// Check es un chequeo declarado para un servicio.
type Check struct {
	Kind   CheckKind
	Target string // URL para HTTP, host:puerto para TCP, nombre para DNS, token para push
	// Resolver (DNS) es el servidor al que se pregunta, host:puerto; vacío
	// es el del hub.
	Resolver string
	Expect   int // HTTP: código esperado; 0 si no se declaró (cualquier 2xx)
	// NoRedirects (HTTP) no sigue las redirecciones: un 3xx es la
	// respuesta.
	NoRedirects bool
	// Interval es cada cuánto se corre (para push, cada cuánto tiene que
	// llegar la llamada) y Timeout, cuánto se espera la respuesta. Cero es
	// el valor por defecto de quien los corre.
	Interval time.Duration
	Timeout  time.Duration
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
	Separate    *bool
	// Hidden oculta (true) o fija como visible (false) la tarjeta en la
	// portada. No cambia nada en el catálogo: lo aplica el hub.
	Hidden *bool
	// FromUI marca un servicio propio creado desde la interfaz, no desde el
	// archivo: sus campos no cuentan como "del archivo" (NameFromFile y
	// los demás quedan en false) y la interfaz lo puede editar y borrar.
	FromUI bool
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
	// true cuando el servicio tiene checks o sale de una unidad de systemd
	// (declarada con unit). AlertFromFile: lo fija maestro.yaml, y la
	// interfaz no lo cambia.
	Alert         bool
	AlertFromFile bool
	// Separate saca al servicio de la tarjeta de su stack en la portada:
	// va en una tarjeta propia.
	Separate bool
	// NameFromFile dice que Name viene de maestro.yaml: un nombre puesto
	// desde la interfaz no lo pisa (archivo > interfaz > labels). Lo mismo
	// GroupFromFile, SeparateFromFile e IconFromFile.
	NameFromFile     bool
	GroupFromFile    bool
	SeparateFromFile bool
	IconFromFile     bool
	// Hidden es el hidden de maestro.yaml; nil si no lo declara.
	Hidden *bool
	// FromUI: servicio propio creado desde la interfaz (ver FileService).
	FromUI bool
}

// Catalog combina las fuentes. Guarda qué claves de labels desconocidas ya
// registró, para avisar de cada una una sola vez en la vida del proceso
// aunque Build se llame cada 15 segundos.
type Catalog struct {
	logger *slog.Logger

	mu       sync.Mutex      // protege avisados; Build puede correr en varias goroutines
	avisados map[string]bool // claves de labels ya avisadas
}

// New crea un catálogo que registra los avisos en logger.
func New(logger *slog.Logger) *Catalog {
	return &Catalog{logger: logger, avisados: make(map[string]bool)}
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
//	Checks       maestro.check.http (+ .expect, .redirects), maestro.check.tcp,
//	             maestro.check.interval, maestro.check.timeout
//	Alert        maestro.alert ("true"/"false", strconv.ParseBool)
//	Ignore       maestro.ignore (ídem)
//	Separate     maestro.separate (ídem)
//
// Un servicio ignorado (por label o por archivo) no aparece. Los contenedores
// con el mismo ID son réplicas de un solo servicio; sus labels se toman de la
// primera réplica por Name. Una clave maestro.* que no está en la tabla, o un
// valor inválido (alert "quizás", expect "abc", un intervalo que no es una
// duración de Go), se ignora y se registra con
// slog en nivel Warn una sola vez por clave. Las claves homepage.*
// desconocidas se ignoran sin avisar (Homepage tiene muchas, de widgets).
//
// Build puede llamarse desde varias goroutines a la vez.
func (c *Catalog) Build(containers []Container, file map[string]FileService) []Service {
	// Agrupa por ID de servicio sin tocar el slice de entrada.
	grupos := make(map[string][]Container)
	for _, ct := range containers {
		id, _ := serviceID(ct)
		grupos[id] = append(grupos[id], ct)
	}

	out := []Service{}
	for id, reps := range grupos {
		slices.SortStableFunc(reps, func(a, b Container) int { return cmp.Compare(a.Name, b.Name) })
		// Solo las claves con "/" (las de compose) pisan a un contenedor.
		var fs FileService
		if strings.Contains(id, "/") {
			fs = file[id]
		}
		if s, ok := c.fromContainers(id, reps, fs); ok {
			out = append(out, s)
		}
	}

	// Servicios propios: claves sin "/".
	for key, fs := range file {
		if strings.Contains(key, "/") || (fs.Ignore != nil && *fs.Ignore) {
			continue
		}
		s := Service{
			ID: key, Source: SourceExternal, Name: cmp.Or(fs.Name, key),
			Group: fs.Group, Icon: fs.Icon, URL: fs.URL, Description: fs.Description,
			Host: fs.Host, Unit: fs.Unit, FromUI: fs.FromUI,
		}
		if !fs.FromUI {
			s.NameFromFile, s.GroupFromFile, s.Hidden = fs.Name != "", fs.Group != "", fs.Hidden
			s.IconFromFile = fs.Icon != ""
		}
		if fs.Unit != "" {
			s.Source = SourceSystemd
		}
		if len(fs.Checks) > 0 {
			s.Checks = slices.Clone(fs.Checks)
		}
		s.Alert = len(s.Checks) > 0 || s.Unit != ""
		if fs.Alert != nil {
			s.Alert, s.AlertFromFile = *fs.Alert, true
		}
		out = append(out, s)
	}

	slices.SortFunc(out, func(a, b Service) int {
		return cmp.Or(cmp.Compare(a.Group, b.Group), cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// serviceID da el ID del servicio al que pertenece un contenedor y si es de
// compose. Es la única regla que decide si un contenedor es de compose.
func serviceID(ct Container) (id string, compose bool) {
	proj, svc := ct.Labels["com.docker.compose.project"], ct.Labels["com.docker.compose.service"]
	if proj != "" && svc != "" {
		return proj + "/" + svc, true
	}
	return ct.Name, false
}

// etiquetasMaestro son las claves maestro.* conocidas.
var etiquetasMaestro = map[string]bool{
	"maestro.name": true, "maestro.group": true, "maestro.icon": true,
	"maestro.url": true, "maestro.description": true,
	"maestro.check.http": true, "maestro.check.http.expect": true,
	"maestro.check.http.redirects": true, "maestro.check.tcp": true,
	"maestro.check.interval": true, "maestro.check.timeout": true,
	"maestro.alert": true, "maestro.ignore": true, "maestro.separate": true,
}

// fromContainers arma el servicio de un grupo de réplicas (ya ordenadas por
// Name). El segundo resultado es false si el servicio está ignorado.
func (c *Catalog) fromContainers(id string, reps []Container, fs FileService) (Service, bool) {
	first := reps[0]
	labels := first.Labels
	c.warnUnknown(labels)

	s := Service{ID: id, Source: SourceContainer, Name: first.Name, Host: first.Host}
	s.Containers = slices.Clone(reps)

	if _, isCompose := serviceID(first); isCompose {
		proj := labels["com.docker.compose.project"]
		s.Source = SourceCompose
		s.Project = proj
		s.Name = labels["com.docker.compose.service"]
		s.Group = proj
		if files := labels["com.docker.compose.project.config_files"]; files != "" {
			s.ComposeFiles = strings.Split(files, ",")
		}
	}

	// Archivo > maestro.* > homepage.* > compose, campo por campo.
	pick := func(file string, maestro, homepage string) string {
		return cmp.Or(file, labels[maestro], labels[homepage])
	}
	s.Name = cmp.Or(fs.Name, labels["maestro.name"], labels["homepage.name"], s.Name)
	s.NameFromFile = fs.Name != ""
	s.Group = cmp.Or(fs.Group, labels["maestro.group"], labels["homepage.group"], s.Group)
	s.GroupFromFile = fs.Group != ""
	s.Hidden = fs.Hidden
	s.Icon = pick(fs.Icon, "maestro.icon", "homepage.icon")
	s.IconFromFile = fs.Icon != ""
	s.URL = pick(fs.URL, "maestro.url", "homepage.href")
	s.Description = pick(fs.Description, "maestro.description", "homepage.description")

	ignore := c.boolLabel(labels, "maestro.ignore")
	if fs.Ignore != nil {
		ignore = fs.Ignore
	}
	if ignore != nil && *ignore {
		return Service{}, false
	}

	if fs.Checks != nil {
		if len(fs.Checks) > 0 {
			s.Checks = slices.Clone(fs.Checks)
		}
	} else {
		s.Checks = c.labelChecks(labels)
	}

	s.Alert = len(s.Checks) > 0
	if a := c.boolLabel(labels, "maestro.alert"); a != nil {
		s.Alert = *a
	}
	if fs.Alert != nil {
		s.Alert, s.AlertFromFile = *fs.Alert, true
	}
	if sep := c.boolLabel(labels, "maestro.separate"); sep != nil {
		s.Separate = *sep
	}
	if fs.Separate != nil {
		s.Separate = *fs.Separate
		s.SeparateFromFile = true
	}
	return s, true
}

// labelChecks lee los checks de los labels; nil si no declara ninguno.
func (c *Catalog) labelChecks(labels map[string]string) []Check {
	var interval, timeout time.Duration
	if v, ok := labels["maestro.check.interval"]; ok {
		interval = c.durationLabel("maestro.check.interval", v)
	}
	if v, ok := labels["maestro.check.timeout"]; ok {
		timeout = c.durationLabel("maestro.check.timeout", v)
	}

	var checks []Check
	if target := labels["maestro.check.http"]; target != "" {
		chk := Check{Kind: CheckHTTP, Target: target, Interval: interval, Timeout: timeout}
		if e, ok := labels["maestro.check.http.expect"]; ok {
			if n, err := strconv.Atoi(e); err == nil && n >= 100 && n <= 599 {
				chk.Expect = n
			} else {
				c.warnOnce("maestro.check.http.expect", "valor inválido en label", "value", e)
			}
		}
		if r := c.boolLabel(labels, "maestro.check.http.redirects"); r != nil {
			chk.NoRedirects = !*r
		}
		checks = append(checks, chk)
	}
	if target := labels["maestro.check.tcp"]; target != "" {
		if _, port, err := net.SplitHostPort(target); err == nil && port != "" {
			checks = append(checks, Check{Kind: CheckTCP, Target: target, Interval: interval, Timeout: timeout})
		} else {
			c.warnOnce("maestro.check.tcp", "valor inválido en label (va host:puerto)", "value", target)
		}
	}
	return checks
}

// durationLabel lee una duración de Go ("30s", "5m"); 0 si es inválida o no
// es positiva (avisa una vez).
func (c *Catalog) durationLabel(key, v string) time.Duration {
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		c.warnOnce(key, "valor inválido en label (va una duración: 30s, 5m)", "value", v)
		return 0
	}
	return d
}

// boolLabel lee un label booleano; nil si falta o es inválido (avisa una vez).
func (c *Catalog) boolLabel(labels map[string]string, key string) *bool {
	v, ok := labels[key]
	if !ok {
		return nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		c.warnOnce(key, "valor inválido en label", "value", v)
		return nil
	}
	return &b
}

// warnUnknown avisa de las claves maestro.* que no están en la tabla.
func (c *Catalog) warnUnknown(labels map[string]string) {
	for k := range labels {
		switch {
		case k == "maestro.check.push":
			c.warnOnce(k, "el check push va en maestro.yaml, no en labels: su token es un secreto")
		case strings.HasPrefix(k, "maestro.") && !etiquetasMaestro[k]:
			c.warnOnce(k, "label maestro.* desconocido, se ignora")
		}
	}
}

// warnOnce registra un Warn la primera vez que ve esa clave.
func (c *Catalog) warnOnce(key, msg string, args ...any) {
	c.mu.Lock()
	visto := c.avisados[key]
	c.avisados[key] = true
	c.mu.Unlock()
	if !visto {
		c.logger.Warn(msg, append([]any{"key", key}, args...)...)
	}
}
