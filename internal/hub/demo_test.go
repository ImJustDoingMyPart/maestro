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
		wantCode int
		wantBody []string
	}{
		{"/", http.StatusOK, []string{"Jellyfin", "AdGuard Home", "Forgejo", `class="dot down"`, "2 caídos"}},
		{"/hardware", http.StatusOK, []string{"Hardware"}},
		{"/discovered", http.StatusOK, []string{"AdGuardHome.service", "Volver a la bandeja", "disabled"}},
		{"/static/beercss-5.0.3/beer.min.css", http.StatusOK, nil},
		{"/static/htmx-2.0.11.min.js", http.StatusOK, nil},
		{"/static/maestro.css", http.StatusOK, []string{".state-pill"}},
		{"/no-existe", http.StatusNotFound, nil},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
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
