package hub

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/auth"
	"github.com/ImJustDoingMyPart/maestro/internal/config"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

// hubWith arma un hub con un agent falso "vm" que tiene containers y la
// configuración cfg, sobre una base vacía.
func hubWith(t *testing.T, cfg *config.Config, containers []agent.Container) (*httptest.Server, *http.Client) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, _ := auth.HashPassword("una-contraseña-larga")
	if err := st.SetPassword(context.Background(), "admin", hash, time.Now()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)
	p := newPoller(map[string]agentClient{"vm": &fakeAgent{cs: containers}}, cfg.FileServices(), logger)
	p.layout = fileLayout{Stacks: cfg.Stacks, Groups: cfg.Groups}
	p.poll(context.Background())
	h, err := newHandler(p, nil, nil, &fakeMonitor{}, st, auth.NewSessions(st, false, logger), testAPIToken, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, loggedIn(t, srv)
}

// portadaDe resume la portada: grupos en orden, con sus tarjetas por clave
// y nombre (sin el orden de las tarjetas, que no se exporta).
func portadaDe(t *testing.T, srv *httptest.Server, c *http.Client) string {
	t.Helper()
	var out []string
	for _, g := range portadaData(t, c, srv).Groups {
		var cards []string
		for _, card := range g.Cards {
			cards = append(cards, card.Key+"="+card.Name)
		}
		slicesSort(cards)
		out = append(out, g.Name+"["+strings.Join(cards, ", ")+"]")
	}
	return strings.Join(out, " ")
}

func slicesSort(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestExportarYVolverACargar(t *testing.T) {
	containers := []agent.Container{
		composeContainer("immich-server", "immich", "server", "running", ""),
		composeContainer("immich-redis", "immich", "redis", "running", ""),
		composeContainer("immich-db", "immich", "db", "running", ""),
		composeContainer("web", "web", "app", "running", ""),
		composeContainer("dns", "red", "dns", "running", ""),
		composeContainer("jelly", "media", "jellyfin", "running", ""),
		composeContainer("qbit", "media", "qbit", "running", ""),
	}
	hosts := "hosts:\n  vm: { agent: http://vm:9101, token: \"${T}\" }\n"
	lookup := func(string) (string, bool) { return "un-token", true }
	base, err := config.Parse([]byte(hosts+"services:\n  web/app: { name: Del archivo }\n"), lookup)
	if err != nil {
		t.Fatal(err)
	}
	srv, c := hubWith(t, base, containers)
	post := func(path string, kv ...string) {
		t.Helper()
		form := url.Values{}
		for i := 0; i < len(kv); i += 2 {
			form.Add(kv[i], kv[i+1])
		}
		res := postForm(t, c, srv.URL+path, form, srv.URL)
		res.Body.Close()
		if res.StatusCode >= 400 {
			t.Fatalf("POST %s %v = %d", path, kv, res.StatusCode)
		}
	}

	// Configuración desde la interfaz, de todo un poco.
	post("/names", "key", "stack:vm/immich", "name", "Fotos")
	post("/groups/move", "key", "stack:vm/immich", "group", "Archivos")
	post("/separate", "key", "service:immich/redis", "separate", "1")
	post("/groups/move", "key", "service:immich/redis", "group", "Otros")
	post("/hidden", "key", "service:red/dns")
	post("/alert", "key", "service:immich/db", "alert", "1")
	post("/alert", "key", "stack:vm/media", "alert", "0")
	post("/hidden", "key", "stack:vm/media")
	post("/groups/move", "key", "service:web/app", "group", "Sitios")
	post("/groups", "name", "Vacío")
	post("/layout/groups", "name", "Vacío", "name", "Sitios", "name", "Archivos")
	post("/externals", "name", "Router", "group", "Red", "kind", "http", "target", "http://192.168.0.1/",
		"expect", "200", "interval", "30s", "url", "http://192.168.0.1")
	antes := portadaDe(t, srv, c)

	res, _ := c.Get(srv.URL + "/export.yaml")
	if res.Header.Get("Content-Disposition") == "" {
		t.Error("export.yaml sin Content-Disposition")
	}
	out := body(t, res)
	for _, no := range []string{"un-token", "agent:", "http://vm"} {
		if strings.Contains(out, no) {
			t.Errorf("el export tiene %q:\n%s", no, out)
		}
	}
	for _, want := range []string{"vm/immich:", "name: Fotos", "group: Otros", "separate: true", "hidden: true", "alert: true", "alert: false", "router:", "interval: 30s"} {
		if !strings.Contains(out, want) {
			t.Errorf("el export no tiene %q:\n%s", want, out)
		}
	}

	// El YAML, junto con hosts y lo que ya tenía el archivo, es un
	// maestro.yaml válido…
	merged := hosts + strings.Replace(out, "  web/app:\n", "  web/app:\n    name: Del archivo\n", 1)
	cfg, err := config.Parse([]byte(merged), lookup)
	if err != nil {
		t.Fatalf("el export no es válido: %v\n%s", err, merged)
	}
	// …y en un hub con la base vacía da la misma portada.
	srv2, c2 := hubWith(t, cfg, containers)
	if despues := portadaDe(t, srv2, c2); despues != antes {
		t.Errorf("la portada cambió al cargar el export:\nantes   %s\ndespués %s\n%s", antes, despues, out)
	}
	// Y lo que vino del archivo queda fijo en la interfaz.
	res = postForm(t, c2, srv2.URL+"/layout/groups", url.Values{"name": {"Archivos", "Vacío"}}, srv2.URL)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Errorf("ordenar grupos fijados por el archivo = %d", res.StatusCode)
	}
	res = postForm(t, c2, srv2.URL+"/hidden/show", url.Values{"key": {"stack:vm/media"}}, srv2.URL)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Errorf("mostrar un oculto del archivo = %d", res.StatusCode)
	}
	if _, b := getPage(t, c2, srv2.URL+"/hidden"); !strings.Contains(b, `"Locked":true`) {
		t.Error("la lista de ocultos no marca los del archivo")
	}
}

func TestExportarVacio(t *testing.T) {
	srv := testServer(t, Snapshot{})
	c := loggedIn(t, srv)
	res, b := getPage(t, c, srv.URL+"/export")
	if res.StatusCode != 200 || !strings.Contains(b, "Todavía no se configuró nada") {
		t.Errorf("export vacío: %d\n%.500s", res.StatusCode, b)
	}
}
