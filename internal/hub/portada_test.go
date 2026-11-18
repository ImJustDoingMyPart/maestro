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

func compSvc(project, name, group, url string) catalog.Service {
	return catalog.Service{ID: project + "/" + name, Source: catalog.SourceCompose, Name: name, Group: group,
		Host: "vm", Project: project, URL: url}
}

func TestBuildGroups(t *testing.T) {
	services := []catalog.Service{
		compSvc("immich", "database", "immich", ""),
		compSvc("immich", "immich-server", "Archivos", "https://fotos"),
		compSvc("immich", "redis", "immich", ""),
		compSvc("media", "jellyfin", "media", ""),
		compSvc("media", "qbittorrent", "media", ""),
		compSvc("solo", "web", "Web", "https://web"),
		{ID: "router", Source: catalog.SourceExternal, Name: "Router"},
		func() catalog.Service { s := compSvc("media", "aparte", "media", ""); s.Separate = true; return s }(),
	}
	states := map[string]status.Status{
		"immich/redis":    {State: status.Degraded, Reasons: []string{"redis unhealthy"}},
		"immich/database": {State: status.Down, Reasons: []string{"database detenido (exit 1)"}},
	}
	statusOf := func(id string) status.Status {
		if s, ok := states[id]; ok {
			return s
		}
		return status.Status{State: status.Up}
	}

	groups, hidden := buildGroups(services, statusOf, uiPrefs{})
	if hidden != 0 {
		t.Errorf("hidden = %d", hidden)
	}
	var got []string
	for _, g := range groups {
		for _, c := range g.Services {
			got = append(got, g.Name+"|"+c.Name+"|"+string(c.State)+"|"+c.Reason)
		}
	}
	want := []string{
		"Archivos|immich-server|down|database: database detenido (exit 1)",
		"media|aparte|up|",
		"media|media|up|",
		"Web|web|up|",
		"Otros|Router|up|",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("tarjetas:\n%s\nquería:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	immich := groups[0].Services[0]
	if immich.URL != "https://fotos" || immich.Key != "stack:vm/immich" || immich.StackHref != "/stacks/vm/immich" ||
		len(immich.Members) != 3 || immich.Description != "3 servicios" || immich.Href != "" {
		t.Errorf("tarjeta de immich: %+v", immich)
	}
	if m := immich.Members[0]; m.Name != "database" || m.State != StateDown || m.Key != "service:immich/database" {
		t.Errorf("primer miembro: %+v", m)
	}

	// Ocultar: un servicio de un stack de dos lo deja como tarjeta suelta;
	// un stack entero desaparece; las claves que no existen no cuentan.
	groups, hidden = buildGroups(services, statusOf, uiPrefs{Hidden: map[string]time.Time{
		"service:media/qbittorrent": {}, "stack:vm/immich": {}, "service:no/existe": {},
	}})
	if hidden != 2 {
		t.Errorf("hidden = %d, quería 2", hidden)
	}
	got = nil
	for _, g := range groups {
		for _, c := range g.Services {
			got = append(got, c.Name+"|"+c.Key)
		}
	}
	want = []string{"aparte|service:media/aparte", "jellyfin|service:media/jellyfin", "web|service:solo/web", "Router|service:router"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("con ocultos:\n got %v\nwant %v", got, want)
	}
}

func TestOcultarYMostrar(t *testing.T) {
	snap := Snapshot{
		At:    time.Now(),
		Hosts: []HostStatus{{Name: "vm", OK: true}},
		Services: []catalog.Service{
			func() catalog.Service {
				s := compSvc("immich", "server", "Archivos", "https://fotos")
				s.Containers = []catalog.Container{{Name: "server", State: "running"}}
				return s
			}(),
			func() catalog.Service {
				s := compSvc("immich", "redis", "immich", "")
				s.Containers = []catalog.Container{{Name: "redis", State: "running"}}
				return s
			}(),
		},
	}
	srv := testServer(t, snap)
	c := loggedIn(t, srv)

	b := portadaBody(t, c, srv)
	for _, want := range []string{`"key":"stack:vm/immich"`, "2 servicios", `"key":"service:immich/redis"`, "/stacks/vm/immich"} {
		if !strings.Contains(b, want) {
			t.Errorf("la portada no tiene %q", want)
		}
	}

	res := postForm(t, c, srv.URL+"/hidden", url.Values{"key": {"stack:vm/immich"}}, srv.URL)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Errorf("ocultar = %d", res.StatusCode)
	}
	b = portadaBody(t, c, srv)
	if strings.Contains(b, `"key":"stack:vm/immich"`) || !strings.Contains(b, `"hiddenCount":1`) {
		t.Errorf("después de ocultar:\n%.300s", b)
	}

	_, b = getPage(t, c, srv.URL+"/hidden")
	if !strings.Contains(b, "immich (vm)") || !strings.Contains(b, "stack de 2 servicios") {
		t.Errorf("la página de ocultos no lo lista:\n%.500s", b)
	}

	// Mostrar, sin JavaScript: vuelve a la lista.
	res = postForm(t, c, srv.URL+"/hidden/show", url.Values{"key": {"stack:vm/immich"}}, srv.URL)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/hidden" {
		t.Errorf("mostrar = %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if b := portadaBody(t, c, srv); !strings.Contains(b, `"key":"stack:vm/immich"`) {
		t.Error("después de mostrar, el stack no volvió")
	}

	// Claves inválidas y otro origen.
	for _, key := range []string{"", "algo", "service:", "usuario:1"} {
		res = postForm(t, c, srv.URL+"/hidden", url.Values{"key": {key}}, srv.URL)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("clave %q = %d", key, res.StatusCode)
		}
	}
	res = postForm(t, c, srv.URL+"/hidden", url.Values{"key": {"stack:vm/immich"}}, "https://otro.example")
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("POST de otro origen = %d", res.StatusCode)
	}
}

func TestStackGroup(t *testing.T) {
	for _, tt := range []struct {
		groups []string
		want   string
	}{
		{[]string{"juguete", "Juguete"}, "Juguete"}, // el declarado, aunque no sea el primero
		{[]string{"juguete", "juguete"}, "juguete"},
		{[]string{"Red", "Otro"}, "Red"},
	} {
		var ms []catalog.Service
		for _, g := range tt.groups {
			ms = append(ms, catalog.Service{Group: g})
		}
		if got, _ := stackGroup("juguete", ms); got != tt.want {
			t.Errorf("stackGroup(%v) = %q, quería %q", tt.groups, got, tt.want)
		}
	}
}

func TestCambiarNombre(t *testing.T) {
	fromFile := compSvc("otro", "app", "Otro", "")
	fromFile.Name, fromFile.NameFromFile = "Del archivo", true
	snap := Snapshot{
		At:    time.Now(),
		Hosts: []HostStatus{{Name: "vm", OK: true}},
		Services: []catalog.Service{
			compSvc("immich", "server", "Archivos", "https://fotos"),
			compSvc("immich", "redis", "immich", ""),
			compSvc("solo", "web", "Web", ""),
			fromFile,
		},
	}
	srv := testServer(t, snap)
	c := loggedIn(t, srv)
	rename := func(key, name string) {
		t.Helper()
		res := postForm(t, c, srv.URL+"/names", url.Values{"key": {key}, "name": {name}}, srv.URL)
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther {
			t.Fatalf("renombrar %s = %d", key, res.StatusCode)
		}
	}
	rename("stack:vm/immich", "  Fotos  ")
	rename("service:solo/web", "Mi web")
	res := postForm(t, c, srv.URL+"/names", url.Values{"key": {"service:otro/app"}, "name": {"No va"}}, srv.URL)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Errorf("renombrar un nombre del archivo = %d", res.StatusCode)
	}

	b := portadaBody(t, c, srv)
	for _, want := range []string{`"name":"Fotos"`, `"name":"Mi web"`, "Del archivo", `"nameLocked":true`} {
		if !strings.Contains(b, want) {
			t.Errorf("la portada no tiene %q", want)
		}
	}
	if strings.Contains(b, "No va") {
		t.Error("un nombre de la interfaz pisó al del archivo")
	}
	// La página del servicio también usa el nombre nuevo.
	if _, b := getPage(t, c, srv.URL+"/services/solo/web"); !strings.Contains(b, "Mi web") {
		t.Error("la página del servicio no usa el nombre nuevo")
	}

	// Vacío vuelve al original.
	rename("service:solo/web", "")
	if b := portadaBody(t, c, srv); strings.Contains(b, "Mi web") || !strings.Contains(b, `"name":"web"`) {
		t.Error("el nombre vacío no volvió al original")
	}

	res = postForm(t, c, srv.URL+"/names", url.Values{"key": {"service:solo/web"}, "name": {strings.Repeat("x", 81)}}, srv.URL)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("nombre de 81 caracteres = %d", res.StatusCode)
	}
}
