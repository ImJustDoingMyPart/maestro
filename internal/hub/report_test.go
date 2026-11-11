package hub

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

// testAPIToken es el token de la API de los servidores de prueba.
const testAPIToken = "token-de-la-api-para-los-tests-0123456789"

// reportServer es un servicio de compose con un secreto en el .env que
// aparece también en el compose, en los logs, en el motivo de un check y en
// la URL de un check.
func reportServer(t *testing.T) (srvURL string, c *http.Client) {
	t.Helper()
	const secret = "Zq8-secretoUnico"
	svc := svcWith("jellyfin")
	svc.Checks = []catalog.Check{{Kind: catalog.CheckHTTP, Target: "http://admin:" + secret + "@jellyfin:8096/health"}}
	snap := Snapshot{Services: []catalog.Service{svc}, Hosts: []HostStatus{{Name: "vm", OK: true}}}
	now := time.Now()
	mon := &fakeMonitor{
		snap: snap,
		checks: map[string][]CheckView{svc.ID: {{
			Kind: catalog.CheckHTTP, Target: svc.Checks[0].Target, Results: 3, Failures: 1,
		}}},
		results: []store.CheckResult{
			{Service: svc.ID, Kind: "http", At: now.Add(-time.Hour), OK: true, Detail: "código 200"},
			{Service: svc.ID, Kind: "http", At: now.Add(-time.Minute), Detail: "dial: password " + secret + " rechazada"},
		},
		events: []store.StateEvent{{Service: svc.ID, At: now.Add(-time.Minute), From: "up", To: "degraded", Reason: "check http falla"}},
	}
	mon.checks[svc.ID][0].Last.At = now.Add(-time.Minute)
	mon.checks[svc.ID][0].Last.Detail = "dial: password " + secret + " rechazada"
	fl := &fakeLogs{history: map[string][]agent.LogLine{"id-jellyfin": {
		line(1, "\x1b[32mINFO\x1b[0m arrancó"),
		line(2, "ERROR conectando con la clave "+secret),
		line(3, "WARN algo lento"),
	}}}
	stacks := &fakeStacks{files: agent.StackFiles{Project: "media", Files: []agent.StackFile{
		{Name: agent.FileCompose, Path: "/stacks/media/compose.yml", Exists: true,
			Content: "services:\n  jellyfin:\n    environment:\n      - API_KEY=${API_KEY}\n      - COPIA=" + secret + "\n"},
		{Name: agent.FileEnv, Path: "/stacks/media/.env", Exists: true, Content: "API_KEY=" + secret + "\nTZ=UTC\n"},
	}}}
	srv := testServerMon(t, snap, fl, stacks, mon)
	return srv.URL, loggedIn(t, srv)
}

func TestReportSinSecretos(t *testing.T) {
	u, c := reportServer(t)
	res, err := c.Get(u + "/api/services/media/jellyfin/report")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("status %d, Content-Type %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	md := body(t, res)
	if strings.Contains(md, "Zq8-secretoUnico") {
		t.Errorf("se escapó el secreto:\n%s", md)
	}
	for _, want := range []string{
		"# Informe de Jellyfin",
		"- **ID**: `media/jellyfin`",
		"## Estado: ",
		"http://admin:[censurado]@jellyfin:8096/health",
		"falla: dial: password [censurado] rechazada",
		"Arriba → Degradado · check http falla",
		"  " + t0.Add(time.Second).UTC().Format("2006-01-02 15:04:05") + " INFO arrancó\n",
		"E ", "1 error y 1 aviso",
		"      - API_KEY=${API_KEY}\n",
		"API_KEY=[censurado]\nTZ=UTC\n",
		"`/stacks/media/.env`", "## compose.yml", "## .env",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("falta %q en:\n%s", want, md)
		}
	}
}

func TestReportAutenticacion(t *testing.T) {
	u, _ := reportServer(t)
	get := func(path, token string) int {
		req, _ := http.NewRequest(http.MethodGet, u+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := client(t).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	tests := []struct {
		name, path, token string
		want              int
	}{
		{"sin nada: 401, no redirige al login", "/api/services/media/jellyfin/report", "", http.StatusUnauthorized},
		{"token equivocado", "/api/services/media/jellyfin/report", "otro-token-de-la-api-0123456789abcdef", http.StatusUnauthorized},
		{"token bueno", "/api/services/media/jellyfin/report", testAPIToken, http.StatusOK},
		{"servicio inexistente", "/api/services/nada/report", testAPIToken, http.StatusNotFound},
		{"sin /report", "/api/services/media/jellyfin", testAPIToken, http.StatusNotFound},
	}
	for _, tt := range tests {
		if got := get(tt.path, tt.token); got != tt.want {
			t.Errorf("%s: %d, quería %d", tt.name, got, tt.want)
		}
	}
}
