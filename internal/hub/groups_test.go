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
	portada := func() string {
		t.Helper()
		res, _ := c.Get(srv.URL + "/")
		return body(t, res)
	}

	if code, _ := post("/groups", url.Values{"name": {" Media "}}); code != http.StatusSeeOther {
		t.Fatalf("crear grupo = %d", code)
	}
	b := portada()
	if i, j := strings.Index(b, `data-group="Web"`), strings.Index(b, `data-group="Media"`); i < 0 || j < i {
		t.Error("el grupo nuevo no va al final")
	}
	if code, _ := post("/groups", url.Values{"name": {"Web"}}); code != http.StatusConflict {
		t.Errorf("crear uno que ya sale de los labels = %d", code)
	}
	if !strings.Contains(b, `class="group empty" data-group="Media"`) || !strings.Contains(b, `<option value="Media">`) {
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
	b = portada()
	for _, want := range []string{`data-group="Media"`, `data-group="Nuevo"`, `data-set-group="Media"`, "El grupo viene de maestro.yaml"} {
		if !strings.Contains(b, want) {
			t.Errorf("la portada no tiene %q", want)
		}
	}

	// Renombrar Web (que sale de los labels) y después borrarlo.
	if code, _ := post("/groups/rename", url.Values{"name": {"Web"}, "to": {"Sitios"}}); code != http.StatusSeeOther {
		t.Fatalf("renombrar = %d", code)
	}
	if b := portada(); strings.Contains(b, `data-group="Web"`) || !strings.Contains(b, `data-group="Sitios"`) {
		t.Error("renombrar Web no lo cambió")
	}
	if code, _ := post("/groups/rename", url.Values{"name": {"Fijo"}, "to": {"X"}}); code != http.StatusConflict {
		t.Errorf("renombrar un grupo del archivo = %d", code)
	}
	if code, _ := post("/groups/delete", url.Values{"name": {"No existe"}}); code != http.StatusNotFound {
		t.Errorf("borrar uno que no existe = %d", code)
	}
	// Con htmx responde la grilla.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/groups/delete", strings.NewReader("name=Sitios"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	req.Header.Set("HX-Request", "true")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b = body(t, res)
	if res.StatusCode != 200 || !strings.HasPrefix(strings.TrimSpace(b), `<div id="services"`) || strings.Contains(b, `data-group="Sitios"`) {
		t.Errorf("borrar con htmx: %d\n%.300s", res.StatusCode, b)
	}
	if !strings.Contains(b, `data-key="service:b/api"`) {
		t.Error("las tarjetas del grupo borrado no pasaron a Otros")
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

	res, _ := c.Get(srv.URL + "/")
	b := body(t, res)
	var keys, groups []string
	for _, part := range strings.Split(b, `<section class="group`)[1:] {
		i := strings.Index(part, `data-group="`) + len(`data-group="`)
		groups = append(groups, part[i:i+strings.Index(part[i:], `"`)])
	}
	for _, part := range strings.Split(b, `class="s12 m6 l4" data-key="`)[1:] {
		keys = append(keys, part[:strings.Index(part, `"`)])
	}
	if got, want := strings.Join(groups, " "), "Web Fijo"; got != want {
		t.Errorf("grupos = %s, quería %s", got, want)
	}
	if got, want := strings.Join(keys, " "), "service:d/cuatro service:c/tres service:a/uno service:b/dos service:cfg/app"; got != want {
		t.Errorf("tarjetas = %s\nquería    %s", got, want)
	}
}
