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

	"github.com/ImJustDoingMyPart/maestro/internal/auth"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

// pollerServer arma el hub con un poller de verdad, sin agents, y el
// archivo file: los externos de la interfaz pasan por el catálogo.
func pollerServer(t *testing.T, file map[string]catalog.FileService) (*httptest.Server, *Poller) {
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
	p := newPoller(nil, file, logger)
	h, err := newHandler(p, nil, nil, &fakeMonitor{}, st, auth.NewSessions(st, false, logger), testAPIToken, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, p
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Router":            "router",
		"  Mi Sitio Web! ":  "mi-sitio-web",
		"Cámara del Jardín": "camara-del-jardin",
		"a/b":               "a-b",
		"---":               "",
		"Ñandú 2":           "nandu-2",
		"日本":                "",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, quería %q", in, got, want)
		}
	}
}

func TestServiciosExternos(t *testing.T) {
	srv, p := pollerServer(t, map[string]catalog.FileService{"router": {Name: "Del archivo"}})
	c := loggedIn(t, srv)
	post := func(path string, form url.Values) (int, string) {
		t.Helper()
		res := postForm(t, c, srv.URL+path, form, srv.URL)
		return res.StatusCode, body(t, res)
	}
	form := func(kv ...string) url.Values {
		v := url.Values{}
		for i := 0; i < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v
	}

	res, _ := c.Get(srv.URL + "/externals/new")
	if b := body(t, res); res.StatusCode != 200 || !strings.Contains(b, `name="target"`) {
		t.Fatalf("formulario nuevo: %d", res.StatusCode)
	}

	// Errores: se juntan y el formulario vuelve con lo escrito.
	code, b := post("/externals", form("name", "", "kind", "tcp", "target", "sin-puerto", "interval", "5 minutos", "url", "https://yo:clave@x.example"))
	if code != http.StatusBadRequest {
		t.Fatalf("con errores = %d", code)
	}
	for _, want := range []string{"Falta el nombre", "Check TCP:", "Intervalo:", "sin usuario ni contraseña", `value="sin-puerto"`} {
		if !strings.Contains(b, want) {
			t.Errorf("faltan %q en:\n%.2000s", want, b)
		}
	}
	if code, b := post("/externals", form("name", "x", "kind", "http", "target", "http://a.example", "expect", "abc")); code != http.StatusBadRequest || !strings.Contains(b, "Código esperado") {
		t.Errorf("expect inválido = %d", code)
	}
	if code, _ := post("/externals", form("name", "x", "kind", "push", "target", "token")); code != http.StatusBadRequest {
		t.Errorf("push desde la interfaz = %d", code)
	}

	// Bien. "Router" choca con la clave del archivo: va router-2.
	code, _ = post("/externals", form("name", "Router", "group", "Red", "kind", "tcp", "target", "192.168.0.1:80", "interval", "30s"))
	if code != http.StatusSeeOther {
		t.Fatalf("crear = %d", code)
	}
	code, _ = post("/externals", form("name", "Mi sitio", "kind", "http", "target", "https://sitio.example/salud", "expect", "204", "url", "https://sitio.example"))
	if code != http.StatusSeeOther {
		t.Fatalf("crear el segundo = %d", code)
	}
	snap := p.Snapshot()
	r2, ok := findService(snap, "router-2")
	if !ok || !r2.FromUI || r2.Source != catalog.SourceExternal ||
		len(r2.Checks) != 1 || r2.Checks[0].Kind != catalog.CheckTCP || r2.Checks[0].Interval != 30*time.Second {
		t.Fatalf("router-2 en el catálogo: %+v", r2)
	}
	sitio, _ := findService(snap, "mi-sitio")
	if len(sitio.Checks) != 1 || sitio.Checks[0].Expect != 204 || !sitio.Checks[0].NoRedirects || sitio.URL != "https://sitio.example" {
		t.Errorf("mi-sitio: %+v", sitio)
	}
	if r, _ := findService(snap, "router"); r.FromUI || r.Name != "Del archivo" {
		t.Errorf("el del archivo cambió: %+v", r)
	}

	res, _ = c.Get(srv.URL + "/clasica")
	b = body(t, res)
	for _, want := range []string{">Router<", `data-group="Red"`, `href="/externals/router-2"`, ">Mi sitio<"} {
		if !strings.Contains(b, want) {
			t.Errorf("la portada no tiene %q", want)
		}
	}

	// Editar: el formulario viene lleno; cambiar el check.
	res, _ = c.Get(srv.URL + "/externals/router-2")
	if b := body(t, res); !strings.Contains(b, `value="Router"`) || !strings.Contains(b, `value="192.168.0.1:80"`) || !strings.Contains(b, `value="30s"`) {
		t.Errorf("el formulario de edición no viene lleno:\n%.1500s", b)
	}
	if code, _ := post("/externals/router-2", form("name", "Router casa", "kind", "http", "target", "http://192.168.0.1/")); code != http.StatusSeeOther {
		t.Fatalf("editar = %d", code)
	}
	r2, _ = findService(p.Snapshot(), "router-2")
	if r2.Checks[0].Kind != catalog.CheckHTTP || r2.Checks[0].Interval != 0 {
		t.Errorf("después de editar: %+v", r2.Checks)
	}
	if code, _ := post("/externals/nada", form("name", "x", "kind", "tcp", "target", "a:1")); code != http.StatusNotFound {
		t.Errorf("editar uno que no existe = %d", code)
	}
	if res, _ := c.Get(srv.URL + "/externals/nada"); res.StatusCode != http.StatusNotFound {
		t.Errorf("página de uno que no existe = %d", res.StatusCode)
	}

	// Borrar.
	if code, _ := post("/externals/router-2/delete", nil); code != http.StatusSeeOther {
		t.Fatalf("borrar = %d", code)
	}
	if _, ok := findService(p.Snapshot(), "router-2"); ok {
		t.Error("el borrado sigue en el catálogo")
	}
}
