package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

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
}

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
}

// Check es un check HTTP. En la etapa 1 se guarda pero no se ejecuta.
type Check struct {
	HTTP   string `yaml:"http"`
	Expect int    `yaml:"expect"` // código esperado; 0 si no se declara
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

	for _, key := range sortedKeys(c.Services) {
		s := c.Services[key]
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
			if err := checkURL(s.Check.HTTP); err != nil {
				add("services.%s.check.http: %v", key, err)
			}
			if s.Check.Expect != 0 && (s.Check.Expect < 100 || s.Check.Expect > 599) {
				add("services.%s.check.expect: %d no es un código HTTP", key, s.Check.Expect)
			}
		}
	}
	return errors.Join(errs...)
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
		}
		if s.Check != nil {
			fs.Checks = []catalog.Check{{Kind: catalog.CheckHTTP, Target: s.Check.HTTP, Expect: s.Check.Expect}}
		}
		out[key] = fs
	}
	return out
}
