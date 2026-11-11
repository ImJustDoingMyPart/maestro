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

	"github.com/ImJustDoingMyPart/maestro/internal/auth"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

func TestCrearPrimerUsuario(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.DiscardHandler)
	snap := Snapshot{}
	h, err := newHandler(fixedSnapshot(snap), nil, nil, &fakeMonitor{snap: snap}, st, auth.NewSessions(st, false, logger), testAPIToken, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := client(t)

	// Sin usuarios, el login ofrece crearlo.
	res, _ := c.Get(srv.URL + "/login")
	if b := body(t, res); !strings.Contains(b, `action="/setup"`) || !strings.Contains(b, "password2") {
		t.Fatal("sin usuarios, /login no ofrece crear uno")
	}

	form := func(user, p1, p2 string) url.Values {
		return url.Values{"username": {user}, "password": {p1}, "password2": {p2}}
	}
	for name, tc := range map[string]struct {
		f    url.Values
		want string
	}{
		"no coinciden":        {form("roberto", "una-contraseña-larga", "otra-contraseña-larga"), "no coinciden"},
		"corta":               {form("roberto", "corta", "corta"), "12 caracteres"},
		"usuario vacío":       {form("", "una-contraseña-larga", "una-contraseña-larga"), "no puede estar vacío"},
		"usuario con espacio": {form("ro berto", "una-contraseña-larga", "una-contraseña-larga"), "espacios"},
	} {
		res := postForm(t, c, srv.URL+"/setup", tc.f, srv.URL)
		if b := body(t, res); res.StatusCode != http.StatusBadRequest || !strings.Contains(b, tc.want) {
			t.Errorf("%s: %d, sin %q", name, res.StatusCode, tc.want)
		}
	}

	// Otro origen: lo frena la protección CSRF.
	res = postForm(t, c, srv.URL+"/setup", form("intruso", "una-contraseña-larga", "una-contraseña-larga"), "https://otro.example")
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("setup de otro origen = %d", res.StatusCode)
	}

	// Bien: crea, entra y va a la portada.
	res = postForm(t, c, srv.URL+"/setup", form("roberto", "una-contraseña-larga", "una-contraseña-larga"), srv.URL)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" {
		t.Fatalf("setup = %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = c.Get(srv.URL + "/")
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("después de crear, la portada = %d (¿no quedó la sesión?)", res.StatusCode)
	}

	// Ya hay usuario: otro navegador ve el login normal y /setup se niega.
	other := client(t)
	res, _ = other.Get(srv.URL + "/login")
	if b := body(t, res); strings.Contains(b, `action="/setup"`) {
		t.Error("con un usuario creado, /login sigue ofreciendo crear")
	}
	res = postForm(t, other, srv.URL+"/setup", form("intruso", "una-contraseña-larga", "una-contraseña-larga"), srv.URL)
	if b := body(t, res); res.StatusCode != http.StatusConflict || !strings.Contains(b, "Ya hay un usuario") {
		t.Errorf("segundo setup = %d", res.StatusCode)
	}
	if _, err := st.UserByName(context.Background(), "intruso"); err == nil {
		t.Error("el segundo setup creó un usuario")
	}
	// Y el creado entra con su contraseña.
	res = postForm(t, other, srv.URL+"/login", url.Values{"username": {"roberto"}, "password": {"una-contraseña-larga"}}, srv.URL)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Errorf("login del usuario creado = %d", res.StatusCode)
	}
}
