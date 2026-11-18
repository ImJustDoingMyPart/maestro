package hub

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
)

// La portada de Svelte lee /api/portada: la misma grilla que la plantilla,
// en JSON, con la clave de cada tarjeta y los servicios de un stack.
func TestPortadaAPI(t *testing.T) {
	snap := Snapshot{
		At:    time.Now(),
		Hosts: []HostStatus{{Name: "vm", OK: true}, {Name: "lxc", Err: "connection refused"}},
		Services: []catalog.Service{
			func() catalog.Service {
				s := compSvc("immich", "server", "Archivos", "https://fotos")
				s.Containers = []catalog.Container{{Name: "server", State: "running"}}
				return s
			}(),
			func() catalog.Service {
				s := compSvc("immich", "redis", "immich", "")
				s.Containers = []catalog.Container{{Name: "redis", State: "exited", ExitCode: 137}}
				return s
			}(),
		},
	}
	srv := testServer(t, snap)

	// Sin sesión no hay datos.
	nc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if res, _ := nc.Get(srv.URL + "/api/portada"); res.StatusCode == http.StatusOK {
		t.Error("/api/portada responde sin sesión")
	}

	c := loggedIn(t, srv)
	res, _ := c.Get(srv.URL + "/api/portada")
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}
	var d apiPortada
	if err := json.NewDecoder(res.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if !d.Live || d.Updated == "" {
		t.Errorf("live=%v updated=%q", d.Live, d.Updated)
	}
	if len(d.HostProblems) != 1 || d.HostProblems[0].Name != "lxc" {
		t.Errorf("hostProblems = %+v", d.HostProblems)
	}
	if len(d.Groups) != 1 || d.Groups[0].Name != "Archivos" || len(d.Groups[0].Cards) != 1 {
		t.Fatalf("grupos = %+v", d.Groups)
	}
	card := d.Groups[0].Cards[0]
	if card.Key != "stack:vm/immich" || len(card.Members) != 2 || card.StateLabel == "" {
		t.Errorf("tarjeta = %+v", card)
	}
	for _, m := range card.Members {
		if m.Key == "" || m.StateLabel == "" {
			t.Errorf("servicio del stack sin clave o estado: %+v", m)
		}
	}

	// "/" es el shell de la aplicación, que pide estos datos.
	res, _ = c.Get(srv.URL + "/")
	if b := body(t, res); !strings.Contains(b, `id="app"`) || !strings.Contains(b, "/static/gen/app/main.js") {
		t.Error("/ no monta la aplicación")
	}
}

// El JSON lleva el interruptor de avisos.
func TestPortadaAPIAvisos(t *testing.T) {
	c := cardJSON(serviceCard{Key: "stack:vm/a", Alert: true, Members: []memberRow{{Key: "service:a/x", AlertLocked: true}}})
	if !c.Alert || c.AlertLocked || !c.Members[0].AlertLocked || c.Members[0].Alert {
		t.Errorf("cardJSON: %+v", c)
	}
}
