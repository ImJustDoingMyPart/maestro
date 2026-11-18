package hub

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/icons"
)

// decorateFound pone los logos de la bandeja.
func decorateFound(r *icons.Resolver, hosts []foundHost, chosen map[string]string) {
	for hi := range hosts {
		for ri := range hosts[hi].Rows {
			row := &hosts[hi].Rows[ri]
			row.IconSet = chosen[row.Key]
			row.Logo, row.Icon = logoFor(r, iconHint{spec: row.IconSet, hints: []string{row.Label, row.Name}}, row.Icon)
		}
	}
}

// foundRow es un descubierto en la página de la bandeja.
type foundRow struct {
	Discovered
	Key  string
	Href string // su página
	Icon string
	Logo *icons.Ref
	// IconSet es el ícono elegido desde la interfaz ("" si no hay).
	IconSet string
	Sub     string // la segunda línea: tipo y motivo o detalle
	Shown   bool
}

// foundHost son los descubiertos de un host.
type foundHost struct {
	Host    string
	Rows    []foundRow
	Failing int // cuántos están caídos o degradados
}

// foundRows arma la bandeja: por host y, dentro, primero lo que falla y
// después por tipo y nombre.
func foundRows(found []Discovered, shown map[string]bool) []foundHost {
	rows := make([]foundRow, 0, len(found))
	for _, d := range found {
		r := foundRow{Discovered: d, Key: d.Key(), Href: servicePath(d.Key()), Icon: kindIcon(d.Kind), Shown: shown[d.Key()]}
		if d.InFile != "" {
			r.Href, r.Shown = servicePath(d.InFile), true
		}
		if r.Label == "" {
			r.Label = r.Name
		}
		switch {
		case d.Reason != "" && d.State != StateUp && d.State != StateStopped:
			// Detenido no lleva motivo: el punto ya lo dice, y la
			// descripción de la unidad dice más que "x detenido".
			r.Sub = foundKindLabel(d.Kind) + " · " + d.Reason
		case d.Kind == "lxc" || d.Kind == "vm":
			r.Sub = cmp.Or(d.Detail, foundKindLabel(d.Kind))
		case d.Detail != "":
			r.Sub = foundKindLabel(d.Kind) + " · " + d.Detail
		default:
			r.Sub = foundKindLabel(d.Kind)
		}
		rows = append(rows, r)
	}
	failing := func(r foundRow) bool { return r.State == StateDown || r.State == StateDegraded }
	slices.SortFunc(rows, func(a, b foundRow) int {
		fa, fb := failing(a), failing(b)
		if a.Host == b.Host && fa != fb {
			if fa {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(a.Host, b.Host), cmp.Compare(kindRank(a.Kind), kindRank(b.Kind)), cmp.Compare(strings.ToLower(a.Label), strings.ToLower(b.Label)))
	})
	var out []foundHost
	for _, r := range rows {
		if len(out) == 0 || out[len(out)-1].Host != r.Host {
			out = append(out, foundHost{Host: r.Host})
		}
		h := &out[len(out)-1]
		h.Rows = append(h.Rows, r)
		if failing(r) {
			h.Failing++
		}
	}
	return out
}

// kindRank ordena los tipos en la bandeja: invitados, timers, servicios.
func kindRank(kind string) int {
	switch kind {
	case "vm", "lxc":
		return 0
	case "timer":
		return 1
	}
	return 2
}

// failingHidden cuenta las unidades que fallan y no están en la portada:
// avisan aunque no se muestren.
func failingHidden(found []Discovered, shown map[string]bool) int {
	n := 0
	for _, d := range found {
		if !shown[d.Key()] && d.InFile == "" && d.State == StateDown {
			n++
		}
	}
	return n
}

// pending cuenta los descubiertos que siguen en la bandeja.
func pending(found []Discovered, shown map[string]bool) int {
	n := 0
	for _, d := range found {
		if !shown[d.Key()] && d.InFile == "" {
			n++
		}
	}
	return n
}

func (s *server) discoveredPage(w http.ResponseWriter, r *http.Request) {
	shown, err := s.prefs.Shown(r.Context())
	if err != nil {
		s.logger.Error("leer mostrados", "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	snap := s.poller.Snapshot()
	found := foundRows(snap.Discovered, shown)
	decorateFound(s.icons, found, s.chosenIcons(r.Context()))
	s.page(w, r, http.StatusOK, pageData{
		Title: "Descubiertos",
		Found: found, FoundPending: pending(snap.Discovered, shown),
	})
}

// foundKey lee y valida la clave de un descubierto.
func foundKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !parseSmallForm(w, r) {
		return "", false
	}
	key := r.PostFormValue("key")
	if rest, ok := strings.CutPrefix(key, "found:"); !ok || strings.Count(rest, "/") < 2 || len(key) > 512 {
		http.Error(w, "clave inválida", http.StatusBadRequest)
		return "", false
	}
	return key, true
}

// showFound atiende POST /discovered/show: lo pasa a la portada. No hace
// falta que exista ahora: un agent caído no lo reporta, y volverá.
func (s *server) showFound(w http.ResponseWriter, r *http.Request) {
	key, ok := foundKey(w, r)
	if !ok {
		return
	}
	if err := s.prefs.Show(r.Context(), key, time.Now()); err != nil {
		s.logger.Error("mostrar descubierto", "key", key, "err", err)
		http.Error(w, "no se pudo mostrar", http.StatusInternalServerError)
		return
	}
	s.syncShown(r.Context())
	http.Redirect(w, r, "/discovered", http.StatusSeeOther)
}

// unshowFound atiende POST /discovered/unshow: lo devuelve a la bandeja.
func (s *server) unshowFound(w http.ResponseWriter, r *http.Request) {
	key, ok := foundKey(w, r)
	if !ok {
		return
	}
	if err := s.prefs.Unshow(r.Context(), key); err != nil {
		s.logger.Error("devolver descubierto", "key", key, "err", err)
		http.Error(w, "no se pudo guardar", http.StatusInternalServerError)
		return
	}
	s.syncShown(r.Context())
	http.Redirect(w, r, "/discovered", http.StatusSeeOther)
}

// syncShown le pasa al poller lo que se muestra ahora, para que lo sume al
// catálogo (y el monitor le calcule el estado) sin esperar la vuelta.
func (s *server) syncShown(ctx context.Context) {
	if s.shown == nil {
		return
	}
	shown, err := s.prefs.Shown(ctx)
	if err != nil {
		s.logger.Error("leer mostrados", "err", err)
		return
	}
	s.shown.SetShown(shown)
}
