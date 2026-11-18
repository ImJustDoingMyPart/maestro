package hub

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/status"
)

// layout resume la portada: "Grupo: tarjeta, tarjeta | Grupo: …", con
// "(vacío)" en los vacíos.
func layout(groups []group) string {
	var parts []string
	for _, g := range groups {
		var names []string
		for _, c := range g.Services {
			names = append(names, c.Name)
		}
		if g.Empty {
			names = append(names, "(vacío)")
		}
		parts = append(parts, g.Name+": "+strings.Join(names, ", "))
	}
	return strings.Join(parts, " | ")
}

func TestBuildGroupsConGruposDeLaInterfaz(t *testing.T) {
	fromFile := compSvc("cfg", "app", "Fijo", "")
	fromFile.GroupFromFile = true
	services := []catalog.Service{
		compSvc("a", "web", "Web", ""),
		compSvc("b", "api", "Web", ""),
		compSvc("c", "db", "", ""),
		compSvc("media", "jellyfin", "media", ""),
		compSvc("media", "qbit", "media", ""),
		fromFile,
	}
	up := func(string) status.Status { return status.Status{State: status.Up} }

	groups, _ := buildGroups(services, up, uiPrefs{
		Groups: map[string]string{
			"service:a/web":   "Media",
			"stack:vm/media":  "Media",
			"service:b/api":   "",       // explícitamente a Otros
			"service:cfg/app": "No va",  // el archivo gana
			"service:no/hay":  "Fantas", // no existe: no crea nada
		},
		GroupOrder: []string{"Vacío", "Media", "Otros"},
	})
	want := "Vacío: (vacío) | Media: media, web | Fijo: app | Otros: api, db"
	if got := layout(groups); got != want {
		t.Errorf("portada:\n got %s\nwant %s", got, want)
	}
	for _, g := range groups {
		if g.Locked != (g.Name == "Fijo") || g.Otros != (g.Name == "Otros") {
			t.Errorf("grupo %s: Locked=%v Otros=%v", g.Name, g.Locked, g.Otros)
		}
	}

	// Un stack toma el bloqueo del servicio del que saca el grupo.
	m1, m2 := compSvc("s", "uno", "s", ""), compSvc("s", "dos", "Red", "")
	m2.GroupFromFile = true
	groups, _ = buildGroups([]catalog.Service{m1, m2}, up, uiPrefs{Groups: map[string]string{"stack:vm/s": "Otro"}})
	if got := layout(groups); got != "Red: s" || !groups[0].Services[0].GroupLocked {
		t.Errorf("stack con grupo del archivo: %s, %+v", got, groups[0].Services[0])
	}
}

func TestGruposDesdeLaInterfaz(t *testing.T) {
	fromFile := compSvc("cfg", "app", "Fijo", "")
	fromFile.GroupFromFile = true
	snap := Snapshot{
		At:    time.Now(),
		Hosts: []HostStatus{{Name: "vm", OK: true}},
		Services: []catalog.Service{
			compSvc("a", "web", "Web", ""),
			compSvc("b", "api", "Web", ""),
			compSvc("c", "db", "", ""),
			fromFile,
		},
	}
	srv := testServer(t, snap)
	c := loggedIn(t, srv)
	post := func(path string, form url.Values) (int, string) {
		t.Helper()
		res := postForm(t, c, srv.URL+path, form, srv.URL)
		return res.StatusCode, body(t, res)
	}
	groupNamed := func(name string) (apiGroup, int) {
		t.Helper()
		for i, g := range portadaData(t, c, srv).Groups {
			if g.Name == name {
				return g, i
			}
		}
		return apiGroup{}, -1
	}

	if code, _ := post("/groups", url.Values{"name": {" Media "}}); code != http.StatusSeeOther {
		t.Fatalf("crear grupo = %d", code)
	}
	if _, i := groupNamed("Web"); i < 0 {
		t.Error("falta Web")
	} else if _, j := groupNamed("Media"); j < i {
		t.Error("el grupo nuevo no va al final")
	}
	if code, _ := post("/groups", url.Values{"name": {"Web"}}); code != http.StatusConflict {
		t.Errorf("crear uno que ya sale de los labels = %d", code)
	}
	if g, _ := groupNamed("Media"); !g.Empty {
		t.Error("el grupo nuevo no sale vacío en la portada")
	}
	for name, tc := range map[string]struct {
		form url.Values
		code int
	}{
		"repetido": {url.Values{"name": {"Media"}}, http.StatusConflict},
		"vacío":    {url.Values{"name": {"  "}}, http.StatusBadRequest},
		"Otros":    {url.Values{"name": {"otros"}}, http.StatusBadRequest},
		"largo":    {url.Values{"name": {strings.Repeat("x", 41)}}, http.StatusBadRequest},
	} {
		if code, _ := post("/groups", tc.form); code != tc.code {
			t.Errorf("crear %s = %d, quería %d", name, code, tc.code)
		}
	}

	// Mover: a un grupo, a Otros y de vuelta al original.
	if code, _ := post("/groups/move", url.Values{"key": {"service:a/web"}, "group": {"Media"}}); code != http.StatusSeeOther {
		t.Fatalf("mover = %d", code)
	}
	if code, _ := post("/groups/move", url.Values{"key": {"service:c/db"}, "group": {"Nuevo"}}); code != http.StatusSeeOther {
		t.Fatalf("mover a uno nuevo = %d", code)
	}
	if code, _ := post("/groups/move", url.Values{"key": {"service:cfg/app"}, "group": {"Media"}}); code != http.StatusConflict {
		t.Errorf("mover una tarjeta con grupo del archivo = %d", code)
	}
	p := portadaData(t, c, srv)
	if card, _, g, _ := findAPICard(p, "service:a/web"); g != "Media" || card.Group != "Media" {
		t.Errorf("web está en %q (Group %q), quería Media", g, card.Group)
	}
	if _, _, g, _ := findAPICard(p, "service:c/db"); g != "Nuevo" {
		t.Errorf("db está en %q, quería Nuevo", g)
	}
	if card, _, _, _ := findAPICard(p, "service:cfg/app"); !card.GroupLocked {
		t.Error("la del archivo no tiene el grupo fijo")
	}

	// Renombrar Web (que sale de los labels) y después borrarlo.
	if code, _ := post("/groups/rename", url.Values{"name": {"Web"}, "to": {"Sitios"}}); code != http.StatusSeeOther {
		t.Fatalf("renombrar = %d", code)
	}
	if _, i := groupNamed("Web"); i >= 0 {
		t.Error("renombrar Web no lo cambió")
	} else if _, j := groupNamed("Sitios"); j < 0 {
		t.Error("renombrar Web no lo cambió")
	}
	if code, _ := post("/groups/rename", url.Values{"name": {"Fijo"}, "to": {"X"}}); code != http.StatusConflict {
		t.Errorf("renombrar un grupo del archivo = %d", code)
	}
	if code, _ := post("/groups/delete", url.Values{"name": {"No existe"}}); code != http.StatusNotFound {
		t.Errorf("borrar uno que no existe = %d", code)
	}
	if code, _ := post("/groups/delete", url.Values{"name": {"Sitios"}}); code != http.StatusSeeOther {
		t.Fatalf("borrar = %d", code)
	}
	if _, i := groupNamed("Sitios"); i >= 0 {
		t.Error("el grupo borrado sigue")
	}
	if _, _, g, _ := findAPICard(portadaData(t, c, srv), "service:b/api"); g != "Otros" {
		t.Errorf("las tarjetas del grupo borrado no pasaron a Otros: %q", g)
	}

	if code, _ := post("/groups/rename", url.Values{"name": {"Otros"}, "to": {"X"}}); code != http.StatusConflict {
		t.Errorf("renombrar Otros = %d", code)
	}
	if code, _ := post("/groups/move", url.Values{"key": {"service:c/db"}, "original": {"1"}}); code != http.StatusSeeOther {
		t.Fatalf("grupo original = %d", code)
	}
}

func TestOrdenarArrastrando(t *testing.T) {
	fromFile := compSvc("cfg", "app", "Fijo", "")
	fromFile.GroupFromFile = true
	snap := Snapshot{
		At:    time.Now(),
		Hosts: []HostStatus{{Name: "vm", OK: true}},
		Services: []catalog.Service{
			compSvc("a", "uno", "Web", ""),
			compSvc("b", "dos", "Web", ""),
			compSvc("c", "tres", "Web", ""),
			compSvc("d", "cuatro", "Red", ""),
			fromFile,
		},
	}
	srv := testServer(t, snap)
	c := loggedIn(t, srv)
	post := func(path string, form url.Values) int {
		t.Helper()
		res := postForm(t, c, srv.URL+path, form, srv.URL)
		res.Body.Close()
		return res.StatusCode
	}

	// Dentro de un grupo.
	if code := post("/layout/cards", url.Values{"group": {"Web"}, "key": {"service:c/tres", "service:a/uno", "service:b/dos"}}); code != http.StatusNoContent {
		t.Fatalf("ordenar = %d", code)
	}
	// De Red a Web: cambia de grupo.
	if code := post("/layout/cards", url.Values{"group": {"Web"}, "key": {"service:d/cuatro", "service:c/tres", "service:a/uno", "service:b/dos"}}); code != http.StatusNoContent {
		t.Fatalf("mover entre grupos = %d", code)
	}
	// Una con el grupo del archivo no se mueve.
	if code := post("/layout/cards", url.Values{"group": {"Web"}, "key": {"service:cfg/app"}}); code != http.StatusConflict {
		t.Errorf("arrastrar una tarjeta con grupo del archivo = %d", code)
	}
	if code := post("/layout/cards", url.Values{"group": {"Web"}}); code != http.StatusBadRequest {
		t.Errorf("sin tarjetas = %d", code)
	}
	// Grupos: Otros no cuenta.
	if code := post("/layout/groups", url.Values{"name": {"Web", "Otros", "Fijo"}}); code != http.StatusNoContent {
		t.Fatalf("ordenar grupos = %d", code)
	}
	if code := post("/layout/groups", url.Values{"name": {"Otros"}}); code != http.StatusBadRequest {
		t.Errorf("solo Otros = %d", code)
	}

	var keys, groups []string
	for _, g := range portadaData(t, c, srv).Groups {
		groups = append(groups, g.Name)
		for _, card := range g.Cards {
			keys = append(keys, card.Key)
		}
	}
	if got, want := strings.Join(groups, " "), "Web Fijo"; got != want {
		t.Errorf("grupos = %s, quería %s", got, want)
	}
	if got, want := strings.Join(keys, " "), "service:d/cuatro service:c/tres service:a/uno service:b/dos service:cfg/app"; got != want {
		t.Errorf("tarjetas = %s\nquería    %s", got, want)
	}
}

func TestSacarDelStack(t *testing.T) {
	fixed := compSvc("p", "fijo", "p", "")
	fixed.SeparateFromFile = true
	snap := Snapshot{
		At:    time.Now(),
		Hosts: []HostStatus{{Name: "vm", OK: true}},
		Services: []catalog.Service{
			compSvc("p", "uno", "p", ""),
			compSvc("p", "dos", "p", ""),
			compSvc("p", "tres", "p", ""),
			fixed,
			{ID: "router", Source: catalog.SourceExternal, Name: "Router"},
		},
	}
	srv := testServer(t, snap)
	c := loggedIn(t, srv)
	post := func(form url.Values) int {
		t.Helper()
		res := postForm(t, c, srv.URL+"/separate", form, srv.URL)
		res.Body.Close()
		return res.StatusCode
	}
	cards := func() string {
		t.Helper()
		var keys []string
		for _, g := range portadaData(t, c, srv).Groups {
			for _, card := range g.Cards {
				keys = append(keys, card.Key)
			}
		}
		return strings.Join(keys, " ")
	}

	if got := cards(); got != "stack:vm/p service:router" {
		t.Fatalf("al empezar: %s", got)
	}
	if code := post(url.Values{"key": {"service:p/uno"}, "separate": {"1"}}); code != http.StatusSeeOther {
		t.Fatalf("sacar = %d", code)
	}
	if got := cards(); got != "stack:vm/p service:p/uno service:router" {
		t.Errorf("después de sacar: %s", got)
	}
	if card, _, _, _ := findAPICard(portadaData(t, c, srv), "service:p/uno"); !card.Separated {
		t.Error("la sacada no está marcada para volver al stack")
	}
	if code := post(url.Values{"key": {"service:p/uno"}, "separate": {"0"}}); code != http.StatusSeeOther {
		t.Fatalf("volver = %d", code)
	}
	if got := cards(); got != "stack:vm/p service:router" {
		t.Errorf("después de volver: %s", got)
	}
	for name, tc := range map[string]struct {
		key  string
		code int
	}{
		"del archivo":   {"service:p/fijo", http.StatusConflict},
		"no es compose": {"service:router", http.StatusBadRequest},
		"un stack":      {"stack:vm/p", http.StatusBadRequest},
		"no existe":     {"service:p/nada", http.StatusNotFound},
	} {
		if code := post(url.Values{"key": {tc.key}, "separate": {"1"}}); code != tc.code {
			t.Errorf("%s = %d, quería %d", name, code, tc.code)
		}
	}
}
