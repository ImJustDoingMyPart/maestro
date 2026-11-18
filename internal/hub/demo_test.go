package hub

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ImJustDoingMyPart/maestro/internal/icons"
)

func TestDemoRoutes(t *testing.T) {
	h, err := newDemoHandler(slog.New(slog.DiscardHandler), icons.New(t.TempDir(), slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path     string
		json     bool // como lo pide el router de la aplicación
		wantCode int
		wantBody []string
	}{
		{"/", false, http.StatusOK, []string{`<div id="app" data-demo>`}},
		{"/api/portada", false, http.StatusOK, []string{"Jellyfin", "AdGuard Home", "Forgejo", `"demo":true`}},
		{"/hardware", true, http.StatusOK, []string{`"Title":"Hardware"`}},
		{"/discovered", true, http.StatusOK, []string{"AdGuardHome.service", `"InFile":"adguard"`, `"Shown":true`}},
		{"/muestrario", true, http.StatusOK, []string{`"Muestra":`, `"Cards":`}},
		{"/static/beercss-5.0.3/beer.min.css", false, http.StatusOK, nil},
		{"/static/gen/app/main.js", false, http.StatusOK, nil},
		{"/static/maestro.css", false, http.StatusOK, []string{".state-pill"}},
		{"/no-existe", false, http.StatusNotFound, nil},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.json {
				req.Header.Set("Accept", "application/json")
			}
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantCode {
				t.Fatalf("GET %s = %d, quería %d", tt.path, rec.Code, tt.wantCode)
			}
			body, _ := io.ReadAll(rec.Body)
			for _, want := range tt.wantBody {
				if !strings.Contains(string(body), want) {
					t.Errorf("GET %s: falta %q en la respuesta", tt.path, want)
				}
			}
		})
	}
}

func TestSummarize(t *testing.T) {
	got := summarize(demoGroups)
	want := summary{Up: 10, Degraded: 1, Down: 2, Unknown: 1}
	if got != want {
		t.Errorf("summarize = %+v, quería %+v", got, want)
	}
}
