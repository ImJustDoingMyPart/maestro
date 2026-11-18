package hub

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
)

// hiddenRow es una tarjeta oculta en la lista de ocultos.
type hiddenRow struct {
	Key    string
	Name   string
	Detail string // "stack, 5 servicios", "servicio de immich", "ya no existe"
	At     time.Time
	Locked bool // lo oculta maestro.yaml: no se muestra desde la interfaz
}

// hide atiende POST /hidden: oculta la tarjeta de la clave "key" y vuelve
// a la portada.
func (s *server) hide(w http.ResponseWriter, r *http.Request) {
	key, ok := s.prefKey(w, r)
	if !ok {
		return
	}
	if s.hiddenFromFile(key) != nil {
		http.Error(w, "hidden viene de maestro.yaml", http.StatusConflict)
		return
	}
	if err := s.prefs.Hide(r.Context(), key, time.Now()); err != nil {
		s.logger.Error("ocultar", "key", key, "err", err)
		http.Error(w, "no se pudo ocultar", http.StatusInternalServerError)
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
	if s.hiddenFromFile(key) != nil {
		http.Error(w, "hidden viene de maestro.yaml", http.StatusConflict)
		return
	}
	if err := s.prefs.Unhide(r.Context(), key); err != nil {
		s.logger.Error("mostrar", "key", key, "err", err)
		http.Error(w, "no se pudo mostrar", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/hidden", http.StatusSeeOther)
}

// prefKey lee y valida la clave del formulario: "service:<id>",
// "stack:<host>/<proyecto>" o "found:<host>/<tipo>/<nombre>". No hace falta que exista: un stack bajado se
// puede seguir mostrando u ocultando.
func (s *server) prefKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulario inválido", http.StatusBadRequest)
		return "", false
	}
	key := r.PostFormValue("key")
	kind, rest, _ := strings.Cut(key, ":")
	if (kind != "service" && kind != "stack" && kind != "found") || rest == "" || len(key) > 512 {
		http.Error(w, "clave inválida", http.StatusBadRequest)
		return "", false
	}
	return key, true
}

// hiddenFromFile es el hidden que maestro.yaml declara para la clave; nil
// si no declara nada.
func (s *server) hiddenFromFile(key string) *bool {
	kind, rest, _ := strings.Cut(key, ":")
	switch kind {
	case "service":
		if svc, ok := findService(s.poller.Snapshot(), rest); ok {
			return svc.Hidden
		}
	case "stack":
		return s.layout.Stacks[rest].Hidden
	}
	return nil
}

// nameFromFile dice si maestro.yaml fija el nombre de la clave.
func (s *server) nameFromFile(key string) bool {
	kind, rest, _ := strings.Cut(key, ":")
	switch kind {
	case "service":
		svc, ok := findService(s.poller.Snapshot(), rest)
		return ok && svc.NameFromFile
	case "stack":
		return s.layout.Stacks[rest].Name != ""
	}
	return false
}

// rename atiende POST /names: cambia el nombre de una tarjeta. Vacío
// vuelve al original.
func (s *server) rename(w http.ResponseWriter, r *http.Request) {
	key, ok := s.prefKey(w, r)
	if !ok {
		return
	}
	if s.nameFromFile(key) {
		http.Error(w, "el nombre viene de maestro.yaml", http.StatusConflict)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if utf8.RuneCountInString(name) > 80 {
		http.Error(w, "el nombre puede tener hasta 80 caracteres", http.StatusBadRequest)
		return
	}
	if err := s.prefs.SetName(r.Context(), key, name, time.Now()); err != nil {
		s.logger.Error("cambiar nombre", "key", key, "err", err)
		http.Error(w, "no se pudo cambiar el nombre", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// names lee los nombres puestos desde la interfaz. Si la base falla, se
// muestran los originales.
func (s *server) names(ctx context.Context) map[string]string {
	names, err := s.prefs.Names(ctx)
	if err != nil {
		s.logger.Error("leer nombres", "err", err)
	}
	return names
}

// lookup busca un servicio en la foto del poller, con su nombre de la
// interfaz si tiene.
func (s *server) lookup(ctx context.Context, id string) (catalog.Service, bool) {
	svc, ok := findService(s.poller.Snapshot(), id)
	if !ok {
		return svc, false
	}
	return applyNames([]catalog.Service{svc}, s.names(ctx))[0], true
}

func (s *server) hiddenPage(w http.ResponseWriter, r *http.Request) {
	hidden, err := s.prefs.Hidden(r.Context())
	if err != nil {
		s.logger.Error("leer ocultos", "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	snap := s.poller.Snapshot()
	names := s.names(r.Context())
	snap.Services = applyNames(snap.Services, names)
	var rows []hiddenRow
	// Lo que oculta el archivo también va, sin el botón.
	all := map[string]hiddenRow{}
	for key, at := range hidden {
		all[key] = hiddenRow{At: at}
	}
	for _, svc := range snap.Services {
		if svc.Hidden != nil {
			delete(all, serviceKey(svc.ID))
			if *svc.Hidden {
				all[serviceKey(svc.ID)] = hiddenRow{Locked: true}
			}
		}
	}
	for k, fs := range s.layout.Stacks {
		if fs.Hidden != nil {
			delete(all, "stack:"+k)
			if *fs.Hidden {
				all["stack:"+k] = hiddenRow{Locked: true}
			}
		}
	}
	for key, base := range all {
		row := hiddenRow{Key: key, Name: key, Detail: "ya no existe", At: base.At, Locked: base.Locked}
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
			if n := names[key]; n != "" {
				row.Name = n + " (" + project + ")"
			}
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
	s.page(w, r, http.StatusOK, pageData{Title: "Ocultos", Hidden: rows})
}
