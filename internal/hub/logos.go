package hub

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/icons"
)

// iconHint es lo que hace falta para darle un logo a una tarjeta o a una
// página: el ícono declarado y los nombres con que adivinarlo.
type iconHint struct {
	spec  string
	hints []string
}

// serviceHint arma las pistas de un servicio: su nombre, el nombre del
// servicio en compose, el proyecto, la imagen y la unidad.
func serviceHint(svc catalog.Service) iconHint {
	h := iconHint{spec: svc.Icon, hints: []string{svc.Name}}
	if svc.Project != "" {
		_, name, _ := strings.Cut(svc.ID, "/")
		h.hints = append(h.hints, name, svc.Project)
	}
	if len(svc.Containers) > 0 {
		h.hints = append(h.hints, icons.ImageHints(svc.Containers[0].Image)...)
	}
	if svc.Unit != "" {
		h.hints = append(h.hints, svc.Unit)
	}
	return h
}

// withOverride aplica el ícono elegido desde la interfaz: gana a los
// labels, pero no al archivo (archivo > interfaz > labels).
func (h iconHint) withOverride(icon string, locked bool) iconHint {
	if icon != "" && !locked {
		h.spec = icon
	}
	return h
}

// symbolName es un nombre que puede ser de Material Symbols ("shield",
// "vpn_lock"): un icon: del archivo de antes de los logos.
var symbolName = regexp.MustCompile(`^[a-z0-9_]{2,40}$`)

// logoFor resuelve el logo. Si no hay, devuelve nil y el ícono de Material
// Symbols: el declarado si tiene esa forma, o fallback.
func logoFor(r *icons.Resolver, h iconHint, fallback string) (*icons.Ref, string) {
	if ref, ok := r.Resolve(h.spec, h.hints...); ok {
		return &ref, fallback
	}
	if h.spec != "" && symbolName.MatchString(h.spec) {
		return nil, h.spec
	}
	if h.spec != "" {
		// Declarado pero no está (todavía no llegó el índice, o no existe):
		// se prueba igual con las pistas.
		if ref, ok := r.Resolve("", h.hints...); ok {
			return &ref, fallback
		}
	}
	return nil, fallback
}

// decorateGroups pone los logos de las tarjetas de la portada. chosen son
// los íconos elegidos desde la interfaz, por clave.
func decorateGroups(r *icons.Resolver, groups []group, chosen map[string]string) {
	for gi := range groups {
		for ci := range groups[gi].Services {
			c := &groups[gi].Services[ci]
			h := c.icon
			if h.spec == "" && len(h.hints) == 0 {
				h.hints = []string{c.Name} // el demo
			}
			c.IconSet = chosen[c.Key]
			c.Logo, c.Icon = logoFor(r, h.withOverride(c.IconSet, c.IconLocked), c.Icon)
		}
	}
}

// iconStore guarda los íconos elegidos desde la interfaz. Lo cumple
// *store.Store; sin él (tests con prefs falsas) no se eligen íconos.
type iconStore interface {
	Icons(ctx context.Context) (map[string]string, error)
	SetIcon(ctx context.Context, key, icon string, at time.Time) error
}

// chosenIcons lee los íconos elegidos. Si la base falla, se usan los de
// siempre.
func (s *server) chosenIcons(ctx context.Context) map[string]string {
	st, ok := s.prefs.(iconStore)
	if !ok {
		return nil
	}
	m, err := st.Icons(ctx)
	if err != nil {
		s.logger.Error("leer íconos", "err", err)
	}
	return m
}

// iconSpec es un ícono que se puede guardar: un nombre de las colecciones
// (con o sin prefijo), un Material Symbol o una URL http(s).
var iconSpec = regexp.MustCompile(`^(?:[a-z0-9][a-z0-9._-]{0,99}|https?://\S{1,400})$`)

// setIcon atiende POST /icons/set: pone (o con icon vacío, saca) el ícono
// elegido de una tarjeta y vuelve a la página de donde vino.
func (s *server) setIcon(w http.ResponseWriter, r *http.Request) {
	st, ok := s.prefs.(iconStore)
	if !ok {
		http.Error(w, "sin base", http.StatusNotImplemented)
		return
	}
	key, ok := s.prefKey(w, r)
	if !ok {
		return
	}
	icon := strings.TrimSpace(r.PostFormValue("icon"))
	if icon != "" && (!iconSpec.MatchString(strings.ToLower(icon)) || !utf8.ValidString(icon)) {
		http.Error(w, "ícono inválido: un nombre (adguard-home, sh-aiostreams, mdi-…) o una URL", http.StatusBadRequest)
		return
	}
	if s.iconFromFile(key) {
		http.Error(w, "el ícono viene de maestro.yaml", http.StatusConflict)
		return
	}
	if err := st.SetIcon(r.Context(), key, icon, time.Now()); err != nil {
		s.logger.Error("cambiar ícono", "key", key, "err", err)
		http.Error(w, "no se pudo cambiar el ícono", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, backTo(r.PostFormValue("back")), http.StatusSeeOther)
}

// backTo es la página a la que se vuelve después de un formulario: una ruta
// propia, nunca otro sitio.
func backTo(back string) string {
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") || strings.ContainsAny(back, "\\\r\n") {
		return "/"
	}
	return back
}

func (s *server) iconFromFile(key string) bool {
	kind, rest, _ := strings.Cut(key, ":")
	if kind == "service" {
		svc, ok := findService(s.poller.Snapshot(), rest)
		return ok && svc.IconFromFile
	}
	return false
}

// searchIcons atiende GET /icons/search?q=: los logos que coinciden, para
// el selector del diálogo "Cambiar ícono".
func (s *server) searchIcons(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) > 60 {
		q = q[:60]
	}
	type hit struct {
		Spec    string `json:"spec"`
		Title   string `json:"title"`
		Src     string `json:"src"`
		SrcDark string `json:"srcDark,omitempty"`
	}
	out := []hit{}
	for _, h := range s.icons.Search(q, 24) {
		out = append(out, hit{h.Spec, h.Title, h.Ref.Src, h.Ref.SrcDark})
	}
	writeJSON(w, http.StatusOK, out)
}
