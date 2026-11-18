package hub

import (
	"context"
	"encoding/json"
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

// getPage pide una página de la aplicación como la pide su router: la
// misma URL con Accept: application/json, que trae los datos de la página.
func getPage(t *testing.T, c *http.Client, u string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Accept", "application/json")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res, body(t, res)
}

// portadaBody es el JSON de la portada (/api/portada).
func portadaBody(t *testing.T, c *http.Client, srv *httptest.Server) string {
	t.Helper()
	res, err := c.Get(srv.URL + "/api/portada")
	if err != nil {
		t.Fatal(err)
	}
	return body(t, res)
}

// portadaData es la portada decodificada.
func portadaData(t *testing.T, c *http.Client, srv *httptest.Server) apiPortada {
	t.Helper()
	var p apiPortada
	if err := json.Unmarshal([]byte(portadaBody(t, c, srv)), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// findAPICard busca una tarjeta (o un servicio dentro de una de stack) por
// su clave; devuelve el grupo en que está.
func findAPICard(p apiPortada, key string) (card apiCard, member *apiMember, group string, ok bool) {
	for _, g := range p.Groups {
		for _, c := range g.Cards {
			if c.Key == key {
				return c, nil, g.Name, true
			}
			for _, m := range c.Members {
				if m.Key == key {
					return c, &m, g.Name, true
				}
			}
		}
	}
	return apiCard{}, nil, "", false
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

	// Sin sesión: la portada y sus datos mandan al login.
	for _, u := range []string{"/", "/api/portada", "/hardware"} {
		res, _ := c.Get(srv.URL + u)
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
			t.Fatalf("GET %s sin sesión = %d %s", u, res.StatusCode, res.Header.Get("Location"))
		}
	}

	// El login se ve, sin la navegación.
	res, _ := c.Get(srv.URL + "/login")
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

	// La portada es el shell de la aplicación, con los headers de
	// seguridad; sus datos traen los servicios, el semáforo y el agent
	// caído.
	res, _ = c.Get(srv.URL + "/")
	if b := body(t, res); !strings.Contains(b, `<div id="app"`) || !strings.Contains(b, "/static/gen/app/main.js") {
		t.Errorf("la portada no es el shell de la aplicación:\n%.300s", b)
	}
	if res.Header.Get("X-Frame-Options") != "DENY" || res.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(res.Header.Get("Content-Security-Policy"), "script-src 'self';") {
		t.Error("faltan headers de seguridad")
	}
	b := portadaBody(t, c, srv)
	for _, want := range []string{
		"Jellyfin", "Media", "https://jellyfin.example.org",
		"Otros", "suelto", `"up":1`, `"down":1`,
		`"hostProblems":[{"name":"lxc","err":"connection refused"`,
	} {
		if !strings.Contains(b, want) {
			t.Errorf("falta %q en la portada", want)
		}
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
	res, _ = c.Get(srv.URL + "/api/portada")
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
