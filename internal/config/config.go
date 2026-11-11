package config

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
)

// Config es maestro.yaml ya leído, con las variables expandidas y validado.
type Config struct {
	// Hosts son los hosts con agent, por nombre.
	Hosts map[string]Host `yaml:"hosts"`
	// Services pisan servicios de compose (clave "<proyecto>/<servicio>") o
	// declaran servicios propios (clave sin "/"). Ver catalog.FileService.
	Services map[string]Service `yaml:"services"`
	// Stacks pisan la tarjeta de un stack de compose en la portada; la
	// clave es "<host>/<proyecto>".
	Stacks map[string]Stack `yaml:"stacks"`
	// Groups fija el orden de los grupos de la portada. Los que no están
	// van después, por nombre; "Otros" siempre al final.
	Groups []string `yaml:"groups"`
	// APIToken deja pedir la API (el informe para IA) con
	// "Authorization: Bearer <token>", sin sesión, por ejemplo con curl
	// desde un agente. Vacío: la API solo con sesión. Es un secreto: va en
	// una variable ${...}.
	APIToken string `yaml:"api_token"`
}

// MinAPITokenLen es el largo mínimo del token de la API: da acceso a los
// informes, con su compose censurado y sus logs.
const MinAPITokenLen = 32

// Host es un host con agent.
type Host struct {
	Agent string `yaml:"agent"` // URL base del agent, http o https
	Token string `yaml:"token"` // secreto: siempre de una variable ${...}
}

// String no muestra el token, para que un fmt.Println o un %v en un error
// no lo saque del proceso.
func (h Host) String() string {
	return fmt.Sprintf("{agent: %s, token: [censurado]}", h.Agent)
}

// LogValue hace lo mismo para slog.
func (h Host) LogValue() slog.Value {
	return slog.GroupValue(slog.String("agent", h.Agent), slog.String("token", "[censurado]"))
}

// Stack es una entrada de "stacks:": nombre, grupo y si se oculta la
// tarjeta de un stack. Lo que se declara acá no se cambia desde la
// interfaz.
type Stack struct {
	Name   string `yaml:"name"`
	Group  string `yaml:"group"`
	Hidden *bool  `yaml:"hidden"`
}

// Service es una entrada de "services:".
type Service struct {
	Name        string `yaml:"name"`
	Group       string `yaml:"group"`
	Icon        string `yaml:"icon"`
	URL         string `yaml:"url"`
	Description string `yaml:"description"`
	Host        string `yaml:"host"` // solo servicios propios
	Unit        string `yaml:"unit"` // solo servicios propios; exige host
	Check       *Check `yaml:"check"`
	// Punteros: nil es "no declarado", que no es lo mismo que false.
	Alert  *bool `yaml:"alert"`
	Ignore *bool `yaml:"ignore"`
	// Separate saca a un servicio de compose de la tarjeta de su stack.
	Separate *bool `yaml:"separate"`
	// Hidden oculta (true) la tarjeta en la portada o la fija como
	// visible (false). Distinto de ignore: se sigue vigilando.
	Hidden *bool `yaml:"hidden"`
}

// Check son los checks de un servicio: uno HTTP, uno TCP y uno push como
// mucho, con el mismo intervalo y timeout.
type Check struct {
	HTTP      string `yaml:"http"`
	Expect    int    `yaml:"expect"`    // código esperado; 0 si no se declara
	Redirects *bool  `yaml:"redirects"` // seguir redirecciones; nil es sí
	TCP       string `yaml:"tcp"`       // host:puerto
	// Push es el token que el script pone en /push/<token>. Es un secreto:
	// va en una variable ${...}.
	Push string `yaml:"push"`
	// Interval y Timeout son duraciones de Go ("30s", "96h"). Para push,
	// Interval es cada cuánto tiene que llegar la llamada.
	Interval string `yaml:"interval"`
	Timeout  string `yaml:"timeout"`
}

// MinPushTokenLen es el largo mínimo del token de un check push: se puede
// adivinar por fuerza bruta contra /push/, que no pide login.
const MinPushTokenLen = 16

// String no muestra el token push.
func (c Check) String() string {
	push := ""
	if c.Push != "" {
		push = "[censurado]"
	}
	return fmt.Sprintf("{http: %s, expect: %d, tcp: %s, push: %s, interval: %s, timeout: %s}",
		c.HTTP, c.Expect, c.TCP, push, c.Interval, c.Timeout)
}

// LookupFunc busca una variable de entorno. Es la firma de os.LookupEnv; los
// tests pasan otra.
type LookupFunc func(name string) (string, bool)

// Load lee y valida el archivo en path, con las variables del entorno.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, err := Parse(data, os.LookupEnv)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodifica data, expande las variables con lookup y valida. Una
// clave desconocida es un error, para que una clave mal escrita no pase en
// silencio. Los errores de validación se juntan: se ven todos a la vez.
func Parse(data []byte, lookup LookupFunc) (*Config, error) {
	// Primero a un árbol de nodos: la expansión se hace sobre los valores
	// ya parseados, no sobre el texto, así un token con ":" o "#" no puede
	// cambiar la estructura del YAML.
	cfg := &Config{}
	loader, err := yaml.NewLoader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var root yaml.Node
	if err := loader.Load(&root); errors.Is(err, io.EOF) {
		return cfg, nil // archivo vacío o solo comentarios
	} else if err != nil {
		return nil, err
	}
	if err := expandNode(&root, lookup); err != nil {
		return nil, err
	}
	if err := root.Load(cfg, yaml.WithKnownFields()); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// varRef reconoce ${NOMBRE}. Un "$" que no forma una referencia válida queda
// tal cual.
var varRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandNode recorre el árbol y expande las variables en los valores string.
// Las claves de los mapas no se tocan.
func expandNode(n *yaml.Node, lookup LookupFunc) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.ShortTag() != "!!str" || !strings.Contains(n.Value, "${") {
			return nil
		}
		var missing []string
		n.Value = varRef.ReplaceAllStringFunc(n.Value, func(ref string) string {
			name := ref[2 : len(ref)-1] // "${NOMBRE}" → "NOMBRE"
			v, ok := lookup(name)
			if !ok {
				missing = append(missing, name)
			}
			return v
		})
		if len(missing) > 0 {
			return fmt.Errorf("línea %d: variable de entorno sin definir: %s", n.Line, strings.Join(missing, ", "))
		}
	case yaml.MappingNode:
		// Content alterna clave, valor, clave, valor…
		var errs []error
		for i := 1; i < len(n.Content); i += 2 {
			errs = append(errs, expandNode(n.Content[i], lookup))
		}
		return errors.Join(errs...)
	case yaml.DocumentNode, yaml.SequenceNode:
		var errs []error
		for _, c := range n.Content {
			errs = append(errs, expandNode(c, lookup))
		}
		return errors.Join(errs...)
	}
	return nil
}

func (c *Config) validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	// Recorrido en orden de clave para que los mensajes salgan siempre igual.
	for _, name := range sortedKeys(c.Hosts) {
		h := c.Hosts[name]
		if err := checkURL(h.Agent); err != nil {
			add("hosts.%s.agent: %v", name, err)
		}
		if h.Token == "" {
			add("hosts.%s.token: falta (va en una variable de entorno, ${...})", name)
		}
	}

	if c.APIToken != "" && len(c.APIToken) < MinAPITokenLen {
		add("api_token: tiene que tener al menos %d caracteres (openssl rand -hex 32)", MinAPITokenLen)
	}

	for _, key := range sortedKeys(c.Stacks) {
		host, project, ok := strings.Cut(key, "/")
		if !ok || host == "" || project == "" || strings.Contains(project, "/") {
			add("stacks.%s: la clave va como <host>/<proyecto>", key)
			continue
		}
		if _, ok := c.Hosts[host]; !ok {
			add("stacks.%s: el host %q no está en hosts", key, host)
		}
	}
	seenGroups := map[string]bool{}
	for i, g := range c.Groups {
		switch {
		case strings.TrimSpace(g) == "":
			add("groups[%d]: nombre vacío", i)
		case seenGroups[g]:
			add("groups: %q está repetido", g)
		case strings.EqualFold(g, "Otros"):
			add("groups: \"Otros\" es el grupo de las tarjetas sin grupo y va siempre al final")
		}
		seenGroups[g] = true
	}

	pushTokens := map[string]string{} // token → primer servicio que lo usa
	for _, key := range sortedKeys(c.Services) {
		s := c.Services[key]
		if s.Check != nil && s.Check.Push != "" {
			if other, dup := pushTokens[s.Check.Push]; dup {
				add("services.%s.check.push: el mismo token que %s; cada servicio lleva el suyo", key, other)
			}
			pushTokens[s.Check.Push] = key
		}
		if strings.Contains(key, "/") {
			// Pisa a un servicio de compose: host y unit no aplican, y
			// catalog los ignoraría en silencio.
			if s.Host != "" {
				add("services.%s.host: solo vale en servicios propios (clave sin /)", key)
			}
			if s.Unit != "" {
				add("services.%s.unit: solo vale en servicios propios (clave sin /)", key)
			}
		} else {
			if s.Unit != "" && s.Host == "" {
				add("services.%s: unit necesita host (el agent que lee la unidad)", key)
			}
		}
		if s.Host != "" {
			if _, ok := c.Hosts[s.Host]; !ok {
				add("services.%s.host: %q no está en hosts", key, s.Host)
			}
		}
		if s.Check != nil {
			validateCheck(key, s.Check, add)
		}
	}
	return errors.Join(errs...)
}

func validateCheck(key string, c *Check, add func(string, ...any)) {
	if c.HTTP == "" && c.TCP == "" && c.Push == "" {
		add("services.%s.check: falta http, tcp o push", key)
	}
	if c.HTTP != "" {
		if err := checkURL(c.HTTP); err != nil {
			add("services.%s.check.http: %v", key, err)
		}
	} else if c.Expect != 0 || c.Redirects != nil {
		add("services.%s.check: expect y redirects son de http", key)
	}
	if c.Expect != 0 && (c.Expect < 100 || c.Expect > 599) {
		add("services.%s.check.expect: %d no es un código HTTP", key, c.Expect)
	}
	if c.TCP != "" {
		if _, port, err := net.SplitHostPort(c.TCP); err != nil || port == "" {
			add("services.%s.check.tcp: %q no es host:puerto", key, c.TCP)
		}
	}
	if c.Push != "" && len(c.Push) < MinPushTokenLen {
		add("services.%s.check.push: el token tiene que tener al menos %d caracteres (openssl rand -hex 16)", key, MinPushTokenLen)
	}
	for _, d := range []struct{ name, v string }{{"interval", c.Interval}, {"timeout", c.Timeout}} {
		if d.v == "" {
			continue
		}
		if v, err := time.ParseDuration(d.v); err != nil || v <= 0 {
			add("services.%s.check.%s: %q no es una duración (30s, 5m, 96h)", key, d.name, d.v)
		}
	}
}

// checkURL exige una URL absoluta http o https.
func checkURL(raw string) error {
	if raw == "" {
		return errors.New("falta la URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q no es una URL http(s) absoluta", raw)
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// FileServices convierte "services:" a la forma que recibe catalog.Build.
func (c *Config) FileServices() map[string]catalog.FileService {
	out := make(map[string]catalog.FileService, len(c.Services))
	for key, s := range c.Services {
		fs := catalog.FileService{
			Name:        s.Name,
			Group:       s.Group,
			Icon:        s.Icon,
			URL:         s.URL,
			Description: s.Description,
			Host:        s.Host,
			Unit:        s.Unit,
			Alert:       s.Alert,
			Ignore:      s.Ignore,
			Separate:    s.Separate,
			Hidden:      s.Hidden,
		}
		if s.Check != nil {
			fs.Checks = s.Check.checks()
		}
		out[key] = fs
	}
	return out
}

// checks pasa un Check ya validado a los de catalog, uno por tipo. Nunca
// devuelve nil: un check declarado reemplaza a los de los labels.
func (c *Check) checks() []catalog.Check {
	// Ya validados: un error de parseo acá no puede pasar.
	interval, _ := time.ParseDuration(cmp.Or(c.Interval, "0s"))
	timeout, _ := time.ParseDuration(cmp.Or(c.Timeout, "0s"))
	out := []catalog.Check{}
	if c.HTTP != "" {
		out = append(out, catalog.Check{
			Kind: catalog.CheckHTTP, Target: c.HTTP, Expect: c.Expect,
			NoRedirects: c.Redirects != nil && !*c.Redirects,
			Interval:    interval, Timeout: timeout,
		})
	}
	if c.TCP != "" {
		out = append(out, catalog.Check{Kind: catalog.CheckTCP, Target: c.TCP, Interval: interval, Timeout: timeout})
	}
	if c.Push != "" {
		out = append(out, catalog.Check{Kind: catalog.CheckPush, Target: c.Push, Interval: interval})
	}
	return out
}

// ValidateCheck valida un check con las mismas reglas que el archivo; key
// es el nombre del servicio para los mensajes. La usa la interfaz para los
// servicios externos que se crean ahí.
func ValidateCheck(key string, c *Check) error {
	var errs []error
	validateCheck(key, c, func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) })
	return errors.Join(errs...)
}

// ValidURL dice si raw es una URL http o https absoluta, como las del
// archivo.
func ValidURL(raw string) error { return checkURL(raw) }

// Checks pasa un check ya validado a los de catalog (ver FileServices).
func (c *Check) Checks() []catalog.Check { return c.checks() }
