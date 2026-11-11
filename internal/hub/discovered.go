package hub

import (
	"cmp"
	"net/http"
	"slices"
	"strings"
	"time"
)

// foundRow es un descubierto en la página de la bandeja.
type foundRow struct {
	Discovered
	Key       string
	KindLabel string
	Icon      string
	Shown     bool
}

// foundRows arma la bandeja: por host, tipo y nombre.
func foundRows(found []Discovered, shown map[string]bool) []foundRow {
	rows := make([]foundRow, 0, len(found))
	for _, d := range found {
		rows = append(rows, foundRow{Discovered: d, Key: d.Key(), KindLabel: foundKindLabel(d.Kind), Icon: kindIcon(d.Kind), Shown: shown[d.Key()]})
	}
	slices.SortFunc(rows, func(a, b foundRow) int {
		return cmp.Or(cmp.Compare(a.Host, b.Host), cmp.Compare(a.Kind, b.Kind), cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)))
	})
	return rows
}

// pending cuenta los descubiertos que siguen en la bandeja.
func pending(found []Discovered, shown map[string]bool) int {
	n := 0
	for _, d := range found {
		if !shown[d.Key()] {
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
	s.pages.render(w, s.logger, "discovered", pageData{
		Title: "Descubiertos", Active: "services", ShowNav: true,
		Found: foundRows(snap.Discovered, shown), FoundPending: pending(snap.Discovered, shown),
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
	http.Redirect(w, r, "/discovered", http.StatusSeeOther)
}

// unshowFound atiende POST /discovered/unshow: lo devuelve a la bandeja.
// Desde la portada (htmx) responde la grilla.
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
	if r.Header.Get("HX-Request") == "true" {
		s.servicesFragment(w, r)
		return
	}
	http.Redirect(w, r, "/discovered", http.StatusSeeOther)
}
