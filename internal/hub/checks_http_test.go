package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/checks"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

func TestPushSinLogin(t *testing.T) {
	snap := Snapshot{}
	mon := &fakeMonitor{snap: snap, tokens: map[string]bool{"0123456789abcdef": true}}
	srv := testServerMon(t, snap, nil, nil, mon)
	c := client(t)
	for _, tc := range []struct{ method, token string }{
		{http.MethodGet, "0123456789abcdef"},
		{http.MethodPost, "0123456789abcdef"},
		{http.MethodGet, "otro"}, // la respuesta no dice si existe
		{http.MethodPost, "otro"},
	} {
		req, _ := http.NewRequest(tc.method, srv.URL+"/push/"+tc.token, nil)
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if b := body(t, res); res.StatusCode != http.StatusOK || b != "ok\n" {
			t.Errorf("%s /push/%s = %d %q", tc.method, tc.token, res.StatusCode, b)
		}
	}
}

func TestPortadaConEstadoCombinado(t *testing.T) {
	snap := Snapshot{
		At:    time.Now(),
		Hosts: []HostStatus{{Name: "vm", OK: true}},
		Services: []catalog.Service{
			{ID: "a/enfermo", Source: catalog.SourceCompose, Name: "Enfermo", Group: "G", Host: "vm", Project: "a",
				Containers: []catalog.Container{{Name: "enfermo", State: "running", Health: "unhealthy"}}},
			{ID: "b/parado", Source: catalog.SourceCompose, Name: "Parado", Group: "G", Host: "vm", Project: "b",
				Containers: []catalog.Container{{Name: "parado", State: "exited", ExitCode: 137}}},
		},
	}
	srv := testServer(t, snap)
	c := loggedIn(t, srv)
	res, _ := c.Get(srv.URL + "/")
	b := body(t, res)
	for _, want := range []string{"Degradado", "enfermo unhealthy", "1 degradado", "parado detenido (exit 137)", `<time datetime=`} {
		if !strings.Contains(b, want) {
			t.Errorf("la portada no tiene %q", want)
		}
	}
}

func TestPaginaConChecks(t *testing.T) {
	now := time.Now()
	svc := catalog.Service{ID: "red/router", Source: catalog.SourceExternal, Name: "Router",
		Checks: []catalog.Check{{Kind: catalog.CheckHTTP, Target: "https://192.168.0.1/"}, {Kind: catalog.CheckPush, Target: "secreto-secreto-1"}}}
	snap := Snapshot{Services: []catalog.Service{svc}}
	mon := &fakeMonitor{
		snap: snap,
		checks: map[string][]CheckView{"red/router": {
			{Kind: catalog.CheckHTTP, Target: "https://192.168.0.1/", Results: 10, Failures: 1,
				Last: checks.Result{At: now, Detail: "timeout (10s)", Latency: 10 * time.Second, CertExpiry: now.Add(5 * 24 * time.Hour)}},
			{Kind: catalog.CheckPush, Results: 0},
		}},
		results: []store.CheckResult{
			{Service: "red/router", Kind: "http", At: now.Add(-time.Minute), OK: true},
			{Service: "red/router", Kind: "http", At: now.Add(-30 * time.Second)},
		},
		events: []store.StateEvent{{ID: 1, Service: "red/router", At: now.Add(-time.Hour), To: "down", Reason: "check http falla: timeout (10s)"}},
	}
	srv := testServerMon(t, snap, nil, nil, mon)
	c := loggedIn(t, srv)

	res, _ := c.Get(srv.URL + "/services/red/router")
	b := body(t, res)
	for _, want := range []string{
		`hx-get="/fragments/checks/red/router"`, "https://192.168.0.1/", "timeout (10s)", "10,0 s",
		"50,0 %", "(4 días)", "cert-warn", "Todavía no corrió", "bar partial", "Push",
		"check http falla: timeout (10s)", // el evento, en la lista (externo, sin logs)
	} {
		if !strings.Contains(b, want) {
			t.Errorf("la página no tiene %q", want)
		}
	}
	if strings.Contains(b, "secreto-secreto-1") {
		t.Error("la página muestra el token push")
	}

	res, _ = c.Get(srv.URL + "/fragments/checks/red/router")
	b = body(t, res)
	if !strings.HasPrefix(strings.TrimSpace(b), `<section id="checks"`) || strings.Contains(b, "<html") {
		t.Errorf("el fragmento no es solo el panel:\n%.200s", b)
	}
}

func TestStreamConCambiosDeEstado(t *testing.T) {
	fl := &fakeLogs{
		history: map[string][]agent.LogLine{"id-jellyfin": {line(1, "uno"), line(3, "tres")}},
		live:    map[string]chan agent.LogLine{"id-jellyfin": make(chan agent.LogLine)},
	}
	snap := Snapshot{Services: []catalog.Service{svcWith("jellyfin")}}
	live := make(chan store.StateEvent, 4)
	mon := &fakeMonitor{snap: snap, live: live, events: []store.StateEvent{
		{ID: 7, Service: "media/jellyfin", At: t0.Add(2 * time.Second), From: "up", To: "down", Reason: "jellyfin detenido (exit 137)"},
	}}
	srv := testServerMon(t, snap, fl, nil, mon)
	c := loggedIn(t, srv)

	r := openStream(t, context.Background(), c, srv.URL+"/logs/media/jellyfin", "")
	if l := r.nextLine(t); l.Line != "uno" {
		t.Errorf("primera: %+v", l)
	}
	ev := r.next(t)
	var st stateEvent
	if ev.Event != "state" || json.Unmarshal([]byte(ev.Data), &st) != nil ||
		st.State != "down" || st.Label != "Caído" || st.Reason != "jellyfin detenido (exit 137)" || ev.ID != "" {
		t.Errorf("quería el cambio de estado entre las líneas, llegó %+v", ev)
	}
	if l := r.nextLine(t); l.Line != "tres" {
		t.Errorf("después del estado: %+v", l)
	}

	// En vivo; el repetido (mismo ID que el del historial) no se manda.
	live <- store.StateEvent{ID: 7, At: t0.Add(2 * time.Second), To: "down", Reason: "repetido"}
	live <- store.StateEvent{ID: 8, At: t0.Add(4 * time.Second), From: "down", To: "up", Reason: "arriba"}
	ev = r.next(t)
	if ev.Event != "state" || !strings.Contains(ev.Data, `"state":"up"`) {
		t.Errorf("en vivo: %+v", ev)
	}
}

func TestFragmentoDeEstado(t *testing.T) {
	snap := Snapshot{
		Hosts: []HostStatus{{Name: "vm", OK: true}},
		Services: []catalog.Service{{ID: "a/parado", Source: catalog.SourceCompose, Name: "Parado", Host: "vm",
			Containers: []catalog.Container{{Name: "parado", State: "exited", ExitCode: 1}}}},
	}
	srv := testServer(t, snap)
	c := loggedIn(t, srv)

	res, _ := c.Get(srv.URL + "/services/a/parado")
	b := body(t, res)
	if !strings.Contains(b, `hx-get="/fragments/service/a/parado"`) || strings.Contains(b, "hx-swap-oob") {
		t.Error("la página tiene que pedir el fragmento y no llevar oob")
	}

	res, _ = c.Get(srv.URL + "/fragments/service/a/parado")
	b = body(t, res)
	for _, want := range []string{`id="svc-live"`, "parado detenido (exit 1)", `id="svc-status"`, `hx-swap-oob="true"`, "Caído"} {
		if !strings.Contains(b, want) {
			t.Errorf("el fragmento no tiene %q", want)
		}
	}
	if strings.Contains(b, "<html") {
		t.Error("el fragmento trae la página entera")
	}

	res, _ = c.Get(srv.URL + "/fragments/service/no/existe")
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("servicio inexistente = %d", res.StatusCode)
	}
}
