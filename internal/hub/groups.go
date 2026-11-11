package hub

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

// loadPrefs lee la configuración de la interfaz. Si la base falla en una
// parte, esa parte queda vacía (se muestra lo de los labels) y se registra.
func (s *server) loadPrefs(ctx context.Context) uiPrefs {
	var p uiPrefs
	var err error
	if p.Hidden, err = s.prefs.Hidden(ctx); err != nil {
		s.logger.Error("leer ocultos", "err", err)
	}
	if p.Names, err = s.prefs.Names(ctx); err != nil {
		s.logger.Error("leer nombres", "err", err)
	}
	if p.Groups, err = s.prefs.CardGroups(ctx); err != nil {
		s.logger.Error("leer grupos de tarjetas", "err", err)
	}
	if p.GroupOrder, err = s.prefs.Groups(ctx); err != nil {
		s.logger.Error("leer grupos", "err", err)
	}
	return p
}

// currentGroups arma la portada como la ve ahora la persona.
func (s *server) currentGroups(ctx context.Context) []group {
	p := s.loadPrefs(ctx)
	groups, _ := buildGroups(applyNames(s.poller.Snapshot().Services, p.Names), s.mon.Status, p)
	return groups
}

// groupName lee y valida un nombre de grupo del formulario.
func groupName(w http.ResponseWriter, r *http.Request, field string) (string, bool) {
	name := strings.TrimSpace(r.PostFormValue(field))
	switch {
	case name == "":
		http.Error(w, "falta el nombre del grupo", http.StatusBadRequest)
		return "", false
	case utf8.RuneCountInString(name) > 40:
		http.Error(w, "el nombre del grupo puede tener hasta 40 caracteres", http.StatusBadRequest)
		return "", false
	case strings.EqualFold(name, otros):
		http.Error(w, `"Otros" es el grupo de las tarjetas sin grupo`, http.StatusBadRequest)
		return "", false
	}
	return name, true
}

// parseSmallForm lee un formulario chico (hasta 4 KiB).
func parseSmallForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulario inválido", http.StatusBadRequest)
		return false
	}
	return true
}

// gridOrHome responde a un cambio de la portada: con htmx, la grilla nueva;
// sin JavaScript, vuelve a la portada.
func (s *server) gridOrHome(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		s.servicesFragment(w, r)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// addGroup atiende POST /groups: crea un grupo vacío al final.
func (s *server) addGroup(w http.ResponseWriter, r *http.Request) {
	if !parseSmallForm(w, r) {
		return
	}
	name, ok := groupName(w, r, "name")
	if !ok {
		return
	}
	if err := s.prefs.AddGroup(r.Context(), name); errors.Is(err, store.ErrExists) {
		http.Error(w, "ya hay un grupo con ese nombre", http.StatusConflict)
		return
	} else if err != nil {
		s.logger.Error("crear grupo", "name", name, "err", err)
		http.Error(w, "no se pudo crear el grupo", http.StatusInternalServerError)
		return
	}
	s.gridOrHome(w, r)
}

// findGroup busca un grupo de la portada actual por nombre.
func findGroup(groups []group, name string) (group, bool) {
	for _, g := range groups {
		if g.Name == name {
			return g, true
		}
	}
	return group{}, false
}

// editableGroup busca el grupo "name" del formulario y responde el error si
// no se puede renombrar ni borrar. Devuelve las claves de sus tarjetas.
func (s *server) editableGroup(w http.ResponseWriter, r *http.Request) (name string, keys []string, ok bool) {
	name = strings.TrimSpace(r.PostFormValue("name"))
	g, found := findGroup(s.currentGroups(r.Context()), name)
	switch {
	case !found:
		http.Error(w, "no existe ese grupo", http.StatusNotFound)
		return "", nil, false
	case g.Otros:
		http.Error(w, `"Otros" no se renombra ni se borra`, http.StatusConflict)
		return "", nil, false
	case g.Locked:
		http.Error(w, "el grupo viene de maestro.yaml", http.StatusConflict)
		return "", nil, false
	}
	for _, c := range g.Services {
		keys = append(keys, c.Key)
	}
	return name, keys, true
}

// renameGroup atiende POST /groups/rename: name → to. Si to ya existe, los
// grupos se juntan.
func (s *server) renameGroup(w http.ResponseWriter, r *http.Request) {
	if !parseSmallForm(w, r) {
		return
	}
	to, ok := groupName(w, r, "to")
	if !ok {
		return
	}
	from, keys, ok := s.editableGroup(w, r)
	if !ok {
		return
	}
	if from != to {
		if err := s.prefs.RenameGroup(r.Context(), from, to, keys, time.Now()); err != nil {
			s.logger.Error("renombrar grupo", "from", from, "to", to, "err", err)
			http.Error(w, "no se pudo renombrar el grupo", http.StatusInternalServerError)
			return
		}
	}
	s.gridOrHome(w, r)
}

// deleteGroup atiende POST /groups/delete: sus tarjetas van a "Otros".
func (s *server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	if !parseSmallForm(w, r) {
		return
	}
	name, keys, ok := s.editableGroup(w, r)
	if !ok {
		return
	}
	if err := s.prefs.DeleteGroup(r.Context(), name, keys, time.Now()); err != nil {
		s.logger.Error("borrar grupo", "name", name, "err", err)
		http.Error(w, "no se pudo borrar el grupo", http.StatusInternalServerError)
		return
	}
	s.gridOrHome(w, r)
}

// findCard busca una tarjeta visible por clave.
func findCard(groups []group, key string) (serviceCard, bool) {
	for _, g := range groups {
		for _, c := range g.Services {
			if c.Key == key {
				return c, true
			}
		}
	}
	return serviceCard{}, false
}

// moveCard atiende POST /groups/move: manda la tarjeta "key" al grupo
// "group" (vacío es "Otros"). Con reset=1 vuelve al grupo de los labels.
func (s *server) moveCard(w http.ResponseWriter, r *http.Request) {
	key, ok := s.prefKey(w, r)
	if !ok {
		return
	}
	if c, found := findCard(s.currentGroups(r.Context()), key); found && c.GroupLocked {
		http.Error(w, "el grupo viene de maestro.yaml", http.StatusConflict)
		return
	}
	var err error
	if r.PostFormValue("reset") == "1" {
		err = s.prefs.ResetCardGroup(r.Context(), key)
	} else {
		grp := strings.TrimSpace(r.PostFormValue("group"))
		if grp != "" && !strings.EqualFold(grp, otros) {
			var ok bool
			if grp, ok = groupName(w, r, "group"); !ok {
				return
			}
		} else {
			grp = ""
		}
		err = s.prefs.SetCardGroup(r.Context(), key, grp, time.Now())
	}
	if err != nil {
		s.logger.Error("mover tarjeta", "key", key, "err", err)
		http.Error(w, "no se pudo mover la tarjeta", http.StatusInternalServerError)
		return
	}
	s.gridOrHome(w, r)
}
