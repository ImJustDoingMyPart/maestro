package hub

import (
	"cmp"
	"net/http"
	"slices"
	"strings"
	"time"
)

// hiddenRow es una tarjeta oculta en la lista de ocultos.
type hiddenRow struct {
	Key    string
	Name   string
	Detail string // "stack, 5 servicios", "servicio de immich", "ya no existe"
	At     time.Time
}

// hide atiende POST /hidden: oculta la tarjeta de la clave "key". Con htmx
// devuelve la grilla nueva; sin JavaScript, vuelve a la portada.
func (s *server) hide(w http.ResponseWriter, r *http.Request) {
	key, ok := s.prefKey(w, r)
	if !ok {
		return
	}
	if err := s.prefs.Hide(r.Context(), key, time.Now()); err != nil {
		s.logger.Error("ocultar", "key", key, "err", err)
		http.Error(w, "no se pudo ocultar", http.StatusInternalServerError)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		s.servicesFragment(w, r)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// unhide atiende POST /hidden/show: vuelve a mostrar una tarjeta.
func (s *server) unhide(w http.ResponseWriter, r *http.Request) {
	key, ok := s.prefKey(w, r)
	if !ok {
		return
	}
	if err := s.prefs.Unhide(r.Context(), key); err != nil {
		s.logger.Error("mostrar", "key", key, "err", err)
		http.Error(w, "no se pudo mostrar", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/hidden", http.StatusSeeOther)
}

// prefKey lee y valida la clave del formulario: "service:<id>" o
// "stack:<host>/<proyecto>". No hace falta que exista: un stack bajado se
// puede seguir mostrando u ocultando.
func (s *server) prefKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulario inválido", http.StatusBadRequest)
		return "", false
	}
	key := r.PostFormValue("key")
	kind, rest, _ := strings.Cut(key, ":")
	if (kind != "service" && kind != "stack") || rest == "" || len(key) > 512 {
		http.Error(w, "clave inválida", http.StatusBadRequest)
		return "", false
	}
	return key, true
}

func (s *server) hiddenPage(w http.ResponseWriter, r *http.Request) {
	hidden, err := s.prefs.Hidden(r.Context())
	if err != nil {
		s.logger.Error("leer ocultos", "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	snap := s.poller.Snapshot()
	var rows []hiddenRow
	for key, at := range hidden {
		row := hiddenRow{Key: key, Name: key, Detail: "ya no existe", At: at}
		kind, rest, _ := strings.Cut(key, ":")
		switch kind {
		case "service":
			if svc, ok := findService(snap, rest); ok {
				row.Name, row.Detail = svc.Name, describe(svc)
				if svc.Project != "" {
					row.Detail = "servicio de " + svc.Project
				}
			}
		case "stack":
			host, project, _ := strings.Cut(rest, "/")
			n := 0
			for _, svc := range snap.Services {
				if svc.Host == host && svc.Project == project {
					n++
				}
			}
			row.Name = stackHideLabel(host, project)
			if n > 0 {
				row.Detail = "stack"
				if n > 1 {
					row.Detail = "stack de " + plural(n, "servicio", "servicios")
				}
			}
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b hiddenRow) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	s.pages.render(w, s.logger, "hidden", pageData{Title: "Ocultos", Active: "services", ShowNav: true, Hidden: rows})
}
