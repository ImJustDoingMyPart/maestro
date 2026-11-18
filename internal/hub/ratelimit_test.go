package hub

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
)

func TestFailLimiter(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	l := newFailLimiter(3, 10*time.Minute)
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if b, _ := l.Blocked("a"); b {
			t.Fatalf("frenada después de %d fallas", i)
		}
		l.Fail("a")
		now = now.Add(time.Minute)
	}
	b, wait := l.Blocked("a")
	if !b || wait != 7*time.Minute {
		t.Fatalf("Blocked = %v %v, quería true 7m (la primera falla sale a los 10 min)", b, wait)
	}
	if b, _ := l.Blocked("b"); b {
		t.Error("otra IP no tiene que quedar frenada")
	}
	now = now.Add(7 * time.Minute)
	if b, _ := l.Blocked("a"); b {
		t.Error("con la primera falla fuera de la ventana ya no está frenada")
	}
	l.Fail("a")
	if b, _ := l.Blocked("a"); !b {
		t.Error("una falla más la vuelve a frenar")
	}
	l.Reset("a")
	if b, _ := l.Blocked("a"); b || len(l.fails) != 0 {
		t.Error("Reset tiene que borrar la cuenta")
	}
}

func TestClientIP(t *testing.T) {
	for _, c := range []struct{ remote, xff, want string }{
		{"10.0.0.2:5555", "", "10.0.0.2"},
		{"172.18.0.5:443", "203.0.113.7", "203.0.113.7"},
		{"172.18.0.5:443", "1.2.3.4, 203.0.113.7", "203.0.113.7"}, // lo que manda el cliente queda antes
		{"[::1]:80", "", "::1"},
	} {
		r := httptest.NewRequest("POST", "/login", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientIP(r); got != c.want {
			t.Errorf("clientIP(%q, %q) = %q, quería %q", c.remote, c.xff, got, c.want)
		}
	}
}

func TestLoginFrenaDespuesDeDiezFallas(t *testing.T) {
	srv := testServer(t, Snapshot{Services: []catalog.Service{}})
	c := client(t)
	bad := url.Values{"username": {"admin"}, "password": {"mala"}}
	for i := 0; i < loginMaxFails; i++ {
		res := postForm(t, c, srv.URL+"/login", bad, srv.URL)
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("intento %d = %d, quería 401", i+1, res.StatusCode)
		}
	}
	// Frenado: ni la contraseña buena entra hasta que pase la ventana.
	res := postForm(t, c, srv.URL+"/login", url.Values{"username": {"admin"}, "password": {"una-contraseña-larga"}}, srv.URL)
	b := body(t, res)
	if res.StatusCode != http.StatusTooManyRequests || !strings.Contains(b, "Demasiados intentos") || res.Header.Get("Retry-After") == "" {
		t.Fatalf("después de %d fallas = %d (Retry-After %q)", loginMaxFails, res.StatusCode, res.Header.Get("Retry-After"))
	}
}
