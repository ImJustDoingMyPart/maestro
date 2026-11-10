package agent

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef"

type fakeSource struct {
	cs   []Container
	err  error
	logs *fakeLogs
}

func (f fakeSource) Containers(context.Context) ([]Container, error) { return f.cs, f.err }

func (f fakeSource) Logs(ctx context.Context, id string, opts LogOptions) (iter.Seq2[LogLine, error], error) {
	if f.logs == nil {
		return nil, ErrContainerNotFound
	}
	return f.logs.open(ctx, id, opts)
}

func newTestServer(t *testing.T, src ContainerSource) *httptest.Server {
	t.Helper()
	h, err := NewHandler(src, nil, testToken, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestTokenDebil(t *testing.T) {
	for _, tok := range []string{"", "corto", strings.Repeat("x", MinTokenLen-1)} {
		if _, err := NewHandler(fakeSource{}, nil, tok, slog.New(slog.DiscardHandler)); !errors.Is(err, ErrWeakToken) {
			t.Errorf("NewHandler(token de %d) = %v, quería ErrWeakToken", len(tok), err)
		}
	}
}

func TestAutenticacion(t *testing.T) {
	srv := newTestServer(t, fakeSource{})
	tests := []struct {
		name   string
		header string
		want   int
	}{
		{"sin header", "", http.StatusUnauthorized},
		{"token incorrecto", "Bearer " + strings.Repeat("z", len(testToken)), http.StatusUnauthorized},
		{"token con otro largo", "Bearer abc", http.StatusUnauthorized},
		{"sin Bearer", testToken, http.StatusUnauthorized},
		{"basic", "Basic " + testToken, http.StatusUnauthorized},
		{"correcto", "Bearer " + testToken, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/containers", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != tt.want {
				t.Errorf("status = %d, quería %d", res.StatusCode, tt.want)
			}
			if tt.want == http.StatusUnauthorized && res.Header.Get("WWW-Authenticate") != "Bearer" {
				t.Error("falta WWW-Authenticate: Bearer")
			}
		})
	}
}

// Una ruta inexistente también pide token: sin él, no se puede ni averiguar
// qué rutas hay.
func TestRutaInexistentePideToken(t *testing.T) {
	srv := newTestServer(t, fakeSource{})
	res, err := http.Get(srv.URL + "/no-existe")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, quería 401", res.StatusCode)
	}
}

func TestClienteContenedores(t *testing.T) {
	want := []Container{
		{ID: "a1", Name: "jellyfin", Image: "jellyfin/jellyfin", State: "running", Health: "healthy", Restarts: 2,
			Labels: map[string]string{"com.docker.compose.project": "media-stack"}},
		{ID: "b2", Name: "frigate", Image: "frigate", State: "exited"},
	}
	srv := newTestServer(t, fakeSource{cs: want})

	got, err := NewClient(srv.URL+"/", testToken).Containers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Containers:\n got %+v\nwant %+v", got, want)
	}
}

func TestClienteListaVacia(t *testing.T) {
	srv := newTestServer(t, fakeSource{cs: nil})
	got, err := NewClient(srv.URL, testToken).Containers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("Containers = %+v, quería vacío", got)
	}
}

func TestClienteErrores(t *testing.T) {
	srv := newTestServer(t, fakeSource{err: errors.New("docker caído")})

	_, err := NewClient(srv.URL, testToken).Containers(context.Background())
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("con Docker caído: err = %v, quería un 502", err)
	}
	if err != nil && strings.Contains(err.Error(), "docker caído") {
		t.Errorf("el detalle interno no tiene que salir del agent: %v", err)
	}

	_, err = NewClient(srv.URL, "otro-token-"+testToken).Containers(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("con token incorrecto: err = %v, quería un 401", err)
	}
}
