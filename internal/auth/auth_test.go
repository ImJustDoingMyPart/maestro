package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

func TestHashPassword(t *testing.T) {
	h1, err := HashPassword("correcta")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h1, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Errorf("formato inesperado: %s", h1)
	}
	h2, _ := HashPassword("correcta")
	if h1 == h2 {
		t.Error("dos hashes de la misma contraseña son iguales: falta la sal")
	}

	if ok, err := VerifyPassword("correcta", h1); !ok || err != nil {
		t.Errorf("VerifyPassword correcta = %v, %v", ok, err)
	}
	if ok, err := VerifyPassword("incorrecta", h1); ok || err != nil {
		t.Errorf("VerifyPassword incorrecta = %v, %v", ok, err)
	}
}

// Un hash hecho con otros parámetros se sigue verificando con los suyos:
// subir el costo más adelante no deja afuera a nadie.
func TestVerifyParametrosGuardados(t *testing.T) {
	salt := []byte("saltsaltsaltsalt")
	key := argon2.IDKey([]byte("hola"), salt, 1, 8192, 1, 32)
	old := fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=1$%s$%s", b64.EncodeToString(salt), b64.EncodeToString(key))

	if ok, err := VerifyPassword("hola", old); !ok || err != nil {
		t.Errorf("hash con parámetros viejos = %v, %v", ok, err)
	}
	if ok, _ := VerifyPassword("chau", old); ok {
		t.Error("contraseña incorrecta aceptada con parámetros viejos")
	}
}

func TestVerifyHashInvalido(t *testing.T) {
	for _, bad := range []string{
		"", "texto", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=18$m=1,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=x,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=1,t=1,p=1$!!!$aGFzaA",
		"$argon2id$v=19$m=1,t=1,p=1$c2FsdA$",
	} {
		if _, err := VerifyPassword("x", bad); !errors.Is(err, ErrMalformedHash) {
			t.Errorf("VerifyPassword(%q) = %v, quería ErrMalformedHash", bad, err)
		}
	}
}

func newSessions(t *testing.T) (*Sessions, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h, _ := HashPassword("secreta")
	if err := st.SetPassword(context.Background(), "admin", h, time.Now()); err != nil {
		t.Fatal(err)
	}
	return NewSessions(st, true, slog.New(slog.DiscardHandler)), st
}

func TestLoginYCookie(t *testing.T) {
	s, _ := newSessions(t)
	ctx := context.Background()

	for _, tc := range []struct{ user, pass string }{{"admin", "mala"}, {"nadie", "secreta"}} {
		rec := httptest.NewRecorder()
		if err := s.Login(ctx, rec, tc.user, tc.pass); !errors.Is(err, ErrBadCredentials) {
			t.Errorf("Login(%s, %s) = %v, quería ErrBadCredentials", tc.user, tc.pass, err)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Error("un login fallido puso cookie")
		}
	}

	rec := httptest.NewRecorder()
	if err := s.Login(ctx, rec, "admin", "secreta"); err != nil {
		t.Fatal(err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, quería 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != CookieName || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("atributos de la cookie: %+v", c)
	}
	if c.MaxAge != int(SessionTTL.Seconds()) {
		t.Errorf("MaxAge = %d", c.MaxAge)
	}
	if len(c.Value) < 40 {
		t.Errorf("token corto: %d caracteres", len(c.Value))
	}
}

func TestRequireLogin(t *testing.T) {
	s, _ := newSessions(t)
	rec := httptest.NewRecorder()
	if err := s.Login(context.Background(), rec, "admin", "secreta"); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]

	protegido := s.RequireLogin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFrom(r.Context())
		if !ok || u.Username != "admin" {
			t.Errorf("UserFrom = %+v, %v", u, ok)
		}
		w.WriteHeader(http.StatusTeapot)
	}))

	tests := []struct {
		name       string
		cookie     *http.Cookie
		json       bool // como pide la aplicación
		wantCode   int
		wantHeader string // Location
	}{
		{"sin cookie", nil, false, http.StatusSeeOther, "/login"},
		{"sin cookie, la aplicación", nil, true, http.StatusSeeOther, "/login"},
		{"cookie inventada", &http.Cookie{Name: CookieName, Value: "inventada"}, false, http.StatusSeeOther, "/login"},
		{"cookie válida", cookie, false, http.StatusTeapot, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.cookie != nil {
				req.AddCookie(tt.cookie)
			}
			if tt.json {
				req.Header.Set("Accept", "application/json")
			}
			rec := httptest.NewRecorder()
			protegido.ServeHTTP(rec, req)
			if rec.Code != tt.wantCode {
				t.Errorf("código = %d, quería %d", rec.Code, tt.wantCode)
			}
			if tt.wantHeader != "" {
				if got := rec.Header().Get("Location"); got != tt.wantHeader {
					t.Errorf("redirect = %q, quería %q", got, tt.wantHeader)
				}
			}
		})
	}

	// Después del logout, la misma cookie ya no sirve.
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(cookie)
	out := httptest.NewRecorder()
	if err := s.Logout(out, req); err != nil {
		t.Fatal(err)
	}
	if c := out.Result().Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
		t.Errorf("el logout no borró la cookie: %+v", c)
	}
	if s.LoggedIn(req) {
		t.Error("la sesión sigue vigente después del logout")
	}
}

func TestSesionVencida(t *testing.T) {
	s, _ := newSessions(t)
	rec := httptest.NewRecorder()
	if err := s.Login(context.Background(), rec, "admin", "secreta"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(rec.Result().Cookies()[0])

	s.now = func() time.Time { return time.Now().Add(SessionTTL + time.Minute) }
	if s.LoggedIn(req) {
		t.Error("una sesión de hace más de 30 días sigue vigente")
	}
}
