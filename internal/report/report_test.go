package report

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/loglevel"
)

// Los golden files son el contrato: los escribió a mano quien definió el
// formato. -update los reescribe con la salida actual, solo para cuando el
// formato cambia a propósito.
var update = flag.Bool("update", false, "reescribir los golden files")

func at(hms string) time.Time {
	t, err := time.Parse("2006-01-02 15:04:05", "2026-11-11 "+hms)
	if err != nil {
		panic(err)
	}
	return t
}

func intp(n int) *int { return &n }

// full es un servicio de compose con todo: dos contenedores, dos checks,
// eventos, logs con niveles y los dos archivos.
func full() Report {
	return Report{
		Generated:   at("15:04:05"),
		ID:          "media-stack/jellyfin",
		Name:        "Jellyfin",
		Description: "Películas y series",
		Group:       "Media",
		URL:         "https://jellyfin.example.org",
		Source:      "compose · media-stack",
		Host:        "vm",
		State:       "Degradado",
		StateSince:  at("14:58:00"),
		Reasons:     []string{"check http falla: timeout (10s)", "jellyfin-1 reiniciado (exit 137)"},
		Containers: []Container{
			{Name: "jellyfin-1", Image: "jellyfin/jellyfin:10.11", State: "running", Health: "healthy",
				ExitCode: intp(137), StartedAt: at("14:58:00"), Restarts: 1},
			{Name: "jellyfin-2", Image: "jellyfin/jellyfin:10.11", State: "running", StartedAt: at("09:00:00")},
		},
		WindowHours: 24,
		Checks: []Check{
			{Kind: "HTTP", Target: "https://jellyfin.example.org/health", Last: "falla: timeout (10s)",
				LastAt: at("15:04:00"), Uptime: "99,3 %", CertExpiry: time.Date(2027, 1, 20, 0, 0, 0, 0, time.UTC),
				Failures: []Failure{
					{At: at("15:04:00"), Detail: "timeout (10s)"},
					{At: at("15:03:00"), Detail: "HTTP 502 | Bad Gateway"},
				}},
			{Kind: "Push", Last: "ok", LastAt: at("03:00:12"), Uptime: "100 %"},
		},
		Events: []Event{
			{At: at("02:00:00"), From: "Arriba", To: "Caído", Reason: "check http falla: timeout (10s)"},
			{At: at("02:05:00"), From: "Caído", To: "Arriba"},
			{At: at("14:58:00"), From: "Arriba", To: "Degradado", Reason: "jellyfin-1 reiniciado (exit 137)"},
		},
		LogsTail: 200,
		Logs: []LogLine{
			{At: at("14:58:01"), Container: "jellyfin-1", Level: loglevel.Info, Text: "[INF] Jellyfin listo"},
			{At: at("14:58:02"), Container: "jellyfin-2", Level: loglevel.Warn, Text: "WARN slow query"},
			{At: at("15:03:00"), Container: "jellyfin-1", Level: loglevel.Error, Text: "ERROR db: password=[censurado] rejected"},
			{Container: "jellyfin-1", Level: loglevel.Info, Text: "línea sin hora"},
		},
		Files: []File{
			{Name: "compose.yml", Path: "/home/anon/docker/media-stack/compose.yml", Exists: true, Lang: "yaml",
				Content: "services:\n  jellyfin:\n    image: jellyfin/jellyfin:10.11\n    environment:\n      - API_KEY=[censurado]\n"},
			{Name: ".env", Path: "/home/anon/docker/media-stack/.env", Exists: true, Lang: "dotenv",
				Content: "TZ=UTC\nDB_PASSWORD=[censurado]"},
		},
	}
}

// minimal es un servicio externo sin nada más que un check que todavía no
// corrió.
func minimal() Report {
	return Report{
		Generated:   at("15:04:05"),
		ID:          "router",
		Name:        "Router",
		Source:      "externo",
		State:       "Sin datos",
		Reasons:     []string{"sin datos todavía"},
		WindowHours: 24,
		Checks:      []Check{{Kind: "TCP", Target: "192.168.0.1:80"}},
		LogsNote:    "Un servicio externo no tiene logs: solo checks.",
	}
}

// broken es un servicio de compose cuyo agent no responde: sin checks, sin
// eventos, sin líneas de log y sin archivos, cada uno con su motivo.
func broken() Report {
	return Report{
		Generated:   at("15:04:05"),
		ID:          "immich/redis",
		Name:        "redis",
		Source:      "compose · immich",
		Host:        "vm",
		State:       "Caído",
		StateSince:  at("10:00:00"),
		Reasons:     []string{"immich_redis detenido (exit 1)"},
		Containers:  []Container{{Name: "immich_redis", Image: "valkey/valkey:9", State: "exited", ExitCode: intp(1)}},
		WindowHours: 24,
		LogsTail:    200,
		FilesNote:   "No se pudo leer el stack: el agent no responde.",
	}
}

func TestGolden(t *testing.T) {
	for name, r := range map[string]Report{"full": full(), "minimal": minimal(), "broken": broken()} {
		t.Run(name, func(t *testing.T) {
			got := Render(r)
			path := filepath.Join("testdata", name+".md")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("Render distinto de %s:\n--- got\n%s\n--- want\n%s", path, got, want)
			}
		})
	}
}

// Un archivo o una línea de log con ``` adentro no puede cerrar el bloque
// de código: la cerca es más larga que la tira de backticks más larga del
// contenido.
func TestFence(t *testing.T) {
	r := minimal()
	r.LogsNote = ""
	r.LogsTail = 10
	r.Logs = []LogLine{{Text: "a ```` b"}}
	r.Files = []File{{Name: "compose.yml", Path: "/x/compose.yml", Exists: true, Lang: "yaml",
		Content: "# ```\nservices: {}"}}
	got := Render(r)
	for _, want := range []string{
		"`````text\n  a ```` b\n`````\n",
		"````yaml\n# ```\nservices: {}\n````\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en:\n%s", want, got)
		}
	}
}

// Un "|" o un salto de línea en una celda no rompe la tabla.
func TestTableCells(t *testing.T) {
	r := minimal()
	r.Checks = []Check{{Kind: "HTTP", Target: "http://x/?a=1|2", Last: "falla: uno\ndos"}}
	got := Render(r)
	want := "| HTTP | http://x/?a=1\\|2 | falla: uno dos | — | — | — | — |\n"
	if !strings.Contains(got, want) {
		t.Errorf("falta %q en:\n%s", want, got)
	}
}

// El informe siempre termina con un solo salto de línea.
func TestTrailingNewline(t *testing.T) {
	for _, r := range []Report{full(), minimal(), broken()} {
		got := Render(r)
		if !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") {
			t.Errorf("%s: no termina en un solo \\n: %q", r.ID, got[max(0, len(got)-20):])
		}
	}
}
