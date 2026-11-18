package hub

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"

	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

// La configuración de la interfaz como secciones de maestro.yaml. Cada
// campo es omitempty: solo sale lo que la interfaz cambió.
type exportDoc struct {
	Groups   []string                 `yaml:"groups,omitempty"`
	Services map[string]exportService `yaml:"services,omitempty"`
	Stacks   map[string]exportStack   `yaml:"stacks,omitempty"`
}

type exportService struct {
	Name        string       `yaml:"name,omitempty"`
	Group       string       `yaml:"group,omitempty"`
	URL         string       `yaml:"url,omitempty"`
	Description string       `yaml:"description,omitempty"`
	Hidden      *bool        `yaml:"hidden,omitempty"`
	Separate    *bool        `yaml:"separate,omitempty"`
	Alert       *bool        `yaml:"alert,omitempty"`
	Check       *exportCheck `yaml:"check,omitempty"`
}

type exportStack struct {
	Name   string `yaml:"name,omitempty"`
	Group  string `yaml:"group,omitempty"`
	Hidden *bool  `yaml:"hidden,omitempty"`
}

type exportCheck struct {
	HTTP      string `yaml:"http,omitempty"`
	Expect    int    `yaml:"expect,omitempty"`
	Redirects *bool  `yaml:"redirects,omitempty"`
	TCP       string `yaml:"tcp,omitempty"`
	Interval  string `yaml:"interval,omitempty"`
	Timeout   string `yaml:"timeout,omitempty"`
}

// exportGroup pasa un grupo de la interfaz al archivo: "" (Otros) se
// escribe "Otros", porque en el archivo un grupo vacío es "no declarado".
func exportGroup(g string) string {
	if g == "" {
		return otros
	}
	return g
}

// exportYAML arma el YAML con la configuración hecha desde la interfaz:
// lo que ya fija el archivo no sale (no tuvo efecto), y nada de secretos:
// la interfaz no guarda ninguno (los tokens de los hosts y los push viven
// solo en el archivo).
func (s *server) exportYAML(ctx context.Context, now time.Time) ([]byte, error) {
	p := s.loadPrefs(ctx)
	snap := s.poller.Snapshot()
	externals, err := s.prefs.Externals(ctx)
	if err != nil {
		return nil, err
	}
	doc := exportDoc{Services: map[string]exportService{}, Stacks: map[string]exportStack{}}

	// Claves de servicio que tocó la interfaz.
	keys := map[string]bool{}
	for _, m := range []map[string]string{p.Names, p.Groups} {
		for k := range m {
			keys[k] = true
		}
	}
	for k := range p.Hidden {
		keys[k] = true
	}
	for k := range p.Separate {
		keys[k] = true
	}
	for k := range p.Alerts {
		keys[k] = true
	}
	for _, e := range externals {
		keys[serviceKey(e.ID)] = true
	}

	found := len(p.Shown)
	for key := range keys {
		kind, rest, _ := strings.Cut(key, ":")
		switch kind {
		case "service":
			svc, exists := findService(snap, rest)
			if !exists && s.fileKeys[rest] {
				continue // lo maneja el archivo y no está ahora: no se sabe qué fija
			}
			var out exportService
			if n, ok := p.Names[key]; ok && !svc.NameFromFile {
				out.Name = n
			}
			if g, ok := p.Groups[key]; ok && !svc.GroupFromFile {
				out.Group = exportGroup(g)
			}
			if _, ok := p.Hidden[key]; ok && svc.Hidden == nil {
				t := true
				out.Hidden = &t
			}
			if v, ok := p.Separate[key]; ok && !svc.SeparateFromFile {
				out.Separate = &v
			}
			if v, ok := p.Alerts[key]; ok && !svc.AlertFromFile {
				out.Alert = &v
			}
			if i := slices.IndexFunc(externals, func(e store.External) bool { return e.ID == rest }); i >= 0 {
				if s.fileKeys[rest] {
					continue // el archivo tiene la misma clave y gana: el externo no está en uso
				}
				e := externals[i]
				out.URL, out.Description = e.URL, e.Description
				c := externalCheck(e)
				out.Check = &exportCheck{HTTP: c.HTTP, Expect: c.Expect, Redirects: c.Redirects, TCP: c.TCP, Interval: c.Interval, Timeout: c.Timeout}
				if out.Name == "" {
					out.Name = rest
				}
			}
			if out != (exportService{}) {
				doc.Services[rest] = out
			}
		case "stack":
			fs := p.FileStacks[rest]
			out := doc.Stacks[rest]
			if n, ok := p.Names[key]; ok && fs.Name == "" {
				out.Name = n
			}
			if g, ok := p.Groups[key]; ok && fs.Group == "" {
				out.Group = exportGroup(g)
			}
			if _, ok := p.Hidden[key]; ok && fs.Hidden == nil {
				t := true
				out.Hidden = &t
			}
			if out != (exportStack{}) {
				doc.Stacks[rest] = out
			}
		}
	}

	if len(p.GroupOrder) > 0 && len(p.FileGroups) == 0 {
		groups, _ := buildGroups(applyNames(withShown(snap, p.Shown), p.Names), s.mon.Status, p)
		for _, g := range groups {
			if !g.Otros {
				doc.Groups = append(doc.Groups, g.Name)
			}
		}
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Configuración hecha desde la interfaz de Maestro, exportada el %s.\n", now.UTC().Format("2006-01-02 15:04 UTC"))
	buf.WriteString("#\n# Para fijarla, juntá estas secciones con las del maestro.yaml del hub y\n" +
		"# reinicialo. Lo que queda en el archivo ya no se cambia desde la interfaz.\n" +
		"# No trae hosts ni tokens: la interfaz no guarda secretos. Las claves de\n" +
		"# stacks: son <host>/<proyecto> y el host tiene que estar en hosts:.\n" +
		"# El orden de las tarjetas dentro de cada grupo no se exporta: queda en\n" +
		"# la base del hub.\n")
	if found > 0 {
		buf.WriteString("# Tampoco los " + strconv.Itoa(found) + " descubiertos que se muestran en la portada: el archivo\n" +
			"# todavía no tiene cómo declararlos.\n")
	}
	if len(doc.Groups) == 0 && len(doc.Services) == 0 && len(doc.Stacks) == 0 {
		buf.WriteString("\n# Todavía no se configuró nada desde la interfaz.\n")
		return buf.Bytes(), nil
	}
	buf.WriteString("\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// exportPage atiende GET /export: el YAML en la página, para copiarlo.
func (s *server) exportPage(w http.ResponseWriter, r *http.Request) {
	out, err := s.exportYAML(r.Context(), time.Now())
	if err != nil {
		s.logger.Error("exportar", "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	s.page(w, r, http.StatusOK, pageData{Title: "Exportar", Export: string(out)})
}

// exportFile atiende GET /export.yaml: el mismo YAML para bajar.
func (s *server) exportFile(w http.ResponseWriter, r *http.Request) {
	out, err := s.exportYAML(r.Context(), time.Now())
	if err != nil {
		s.logger.Error("exportar", "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="maestro-interfaz.yaml"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(out)
}
