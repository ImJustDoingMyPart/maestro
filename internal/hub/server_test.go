package hub

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
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

type fixedSnapshot Snapshot

func (f fixedSnapshot) Snapshot() Snapshot { return Snapshot(f) }

func testServer(t *testing.T, snap Snapshot) *httptest.Server {
	t.Helper()
	return testServerLogs(t, snap, nil)
}

func testServerLogs(t *testing.T, snap Snapshot, logs logOpener) *httptest.Server {
	t.Helper()
	return testServerWith(t, snap, logs, nil)
}

func testServerWith(t *testing.T, snap Snapshot, logs logOpener, stacks stackClient) *httptest.Server {
	t.Helper()
	return testServerMon(t, snap, logs, stacks, &fakeMonitor{snap: snap})
}

func testServerMon(t *testing.T, snap Snapshot, logs logOpener, stacks stackClient, mon monitorView) *httptest.Server {
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
	// secure=false: el servidor de prueba es HTTP y el cliente no mandaría
	// una cookie Secure.
	h, err := newHandler(fixedSnapshot(snap), logs, stacks, mon, st, auth.NewSessions(st, false, logger), testAPIToken, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// client no sigue redirects, para poder mirarlos, y guarda cookies.
func client(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// loggedIn devuelve un cliente con sesión iniciada.
func loggedIn(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	c := client(t)
	res := postForm(t, c, srv.URL+"/login", url.Values{"username": {"admin"}, "password": {"una-contraseña-larga"}}, srv.URL)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("login = %d", res.StatusCode)
	}
	return c
}

func postForm(t *testing.T, c *http.Client, u string, form url.Values, origin string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRecorridoDeLogin(t *testing.T) {
	snap := Snapshot{
		At:    time.Unix(1_800_000_000, 0),
		Hosts: []HostStatus{{Name: "vm", OK: true}, {Name: "lxc", Err: "connection refused"}},
		Services: []catalog.Service{
			{ID: "media/jellyfin", Source: catalog.SourceCompose, Name: "Jellyfin", Group: "Media", Host: "vm",
				URL:        "https://jellyfin.example.org",
				Containers: []catalog.Container{{Name: "jellyfin", Image: "jellyfin/jellyfin", State: "running"}}},
			{ID: "suelto", Source: catalog.SourceContainer, Name: "suelto", Host: "vm",
				Containers: []catalog.Container{{Name: "suelto", Image: "alpine", State: "exited"}}},
		},
	}
	srv := testServer(t, snap)
	c := client(t)

	// Sin sesión: la portada manda al login y el fragmento avisa a htmx.
	res, _ := c.Get(srv.URL + "/")
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
		t.Fatalf("GET / sin sesión = %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/fragments/services", nil)
	req.Header.Set("HX-Request", "true")
	res, _ = c.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized || res.Header.Get("HX-Redirect") != "/login" {
		t.Errorf("fragmento sin sesión = %d, HX-Redirect %q", res.StatusCode, res.Header.Get("HX-Redirect"))
	}

	// El login se ve, sin la navegación.
	res, _ = c.Get(srv.URL + "/login")
	if b := body(t, res); !strings.Contains(b, `name="password"`) || strings.Contains(b, "topbar") {
		t.Error("la página de login no es la esperada")
	}

	// Contraseña incorrecta.
	res = postForm(t, c, srv.URL+"/login", url.Values{"username": {"admin"}, "password": {"mala"}}, srv.URL)
	if b := body(t, res); res.StatusCode != http.StatusUnauthorized || !strings.Contains(b, "incorrectos") {
		t.Errorf("login con contraseña mala = %d", res.StatusCode)
	}

	// Un POST de login desde otro origen se rechaza (CSRF).
	res = postForm(t, c, srv.URL+"/login", url.Values{"username": {"admin"}, "password": {"una-contraseña-larga"}}, "https://malicioso.example")
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("POST cross-origin = %d, quería 403", res.StatusCode)
	}

	// Login correcto.
	res = postForm(t, c, srv.URL+"/login", url.Values{"username": {"admin"}, "password": {"una-contraseña-larga"}}, srv.URL)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" {
		t.Fatalf("login correcto = %d %s", res.StatusCode, res.Header.Get("Location"))
	}

	// La portada muestra los servicios, el semáforo y el agent caído.
	res, _ = c.Get(srv.URL + "/")
	b := body(t, res)
	for _, want := range []string{
		"Jellyfin", "Media", "jellyfin/jellyfin", "https://jellyfin.example.org",
		"Otros", "suelto", "1 arriba", "1 caído",
		"El agent de lxc no responde", "connection refused",
		`hx-get="/fragments/services"`, "Actualizado",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("falta %q en la portada", want)
		}
	}
	if res.Header.Get("X-Frame-Options") != "DENY" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("faltan headers de seguridad")
	}

	// El fragmento es solo la grilla.
	res, _ = c.Get(srv.URL + "/fragments/services")
	if b := body(t, res); !strings.HasPrefix(strings.TrimSpace(b), `<div id="services"`) || strings.Contains(b, "<html") {
		t.Errorf("el fragmento no es solo la grilla:\n%.200s", b)
	}

	// Ya logueado, /login manda a la portada.
	res, _ = c.Get(srv.URL + "/login")
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Errorf("GET /login con sesión = %d, quería 303", res.StatusCode)
	}

	// Logout: la sesión deja de servir.
	res = postForm(t, c, srv.URL+"/logout", nil, srv.URL)
	res.Body.Close()
	res, _ = c.Get(srv.URL + "/")
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Errorf("GET / después del logout = %d, quería 303", res.StatusCode)
	}
}

func TestEstaticosSinSesion(t *testing.T) {
	srv := testServer(t, Snapshot{})
	res, err := http.Get(srv.URL + "/static/maestro.css")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("CSS sin sesión = %d", res.StatusCode)
	}
}
