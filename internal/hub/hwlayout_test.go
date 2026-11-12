package hub

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

func TestModoEdicionDeHardware(t *testing.T) {
	m := &agent.HostMetrics{CPU: 5, MemUsed: 1 << 30, MemTotal: 4 << 30, Temps: []agent.TempValue{{Chip: "k10temp", Label: "Tctl", Celsius: 50}}}
	snap := Snapshot{
		Hosts:   []HostStatus{{Name: "vm", OK: true}, {Name: "pve", OK: true}},
		Systems: map[string]agent.System{"vm": {Metrics: m}, "pve": {Metrics: m}},
	}
	srv := testServer(t, snap)
	c := loggedIn(t, srv)
	post := func(path string, form url.Values) int {
		t.Helper()
		res := postForm(t, c, srv.URL+path, form, srv.URL)
		res.Body.Close()
		return res.StatusCode
	}
	page := func(host string) string {
		t.Helper()
		res, _ := c.Get(srv.URL + "/hardware?host=" + host)
		return body(t, res)
	}

	if b := page("vm"); !strings.Contains(b, "data-edit-toggle") || strings.Index(b, `data-hw-item="tile:cpu"`) > strings.Index(b, `data-hw-item="tile:ram"`) {
		t.Fatal("Hardware sin modo edición o con otro orden de siempre")
	}

	// La RAM primero y la CPU y la temperatura ocultas, solo en vm.
	if code := post("/hardware/order", url.Values{"host": {"vm"}, "item": {"tile:ram", "tile:cpu"}}); code != http.StatusNoContent {
		t.Fatalf("orden = %d", code)
	}
	for _, it := range []string{"tile:cpu", "chart:temp", "section:storage"} {
		if code := post("/hardware/hidden", url.Values{"host": {"vm"}, "item": {it}, "hidden": {"1"}}); code != http.StatusNoContent {
			t.Fatalf("ocultar %s = %d", it, code)
		}
	}
	b := page("vm")
	if strings.Index(b, `data-hw-item="tile:ram"`) > strings.Index(b, `data-hw-item="tile:cpu"`) {
		t.Error("no tomó el orden")
	}
	for _, want := range []string{`is-hidden" data-hw-item="tile:cpu"`, `is-hidden" data-chart`} {
		if !strings.Contains(b, want) {
			t.Errorf("falta %q", want)
		}
	}
	if strings.Contains(page("pve"), "is-hidden") {
		t.Error("lo oculto en vm se ocultó en pve")
	}

	// Mostrar de nuevo.
	post("/hardware/hidden", url.Values{"host": {"vm"}, "item": {"tile:cpu"}, "hidden": {"0"}})
	if strings.Contains(page("vm"), `is-hidden" data-hw-item="tile:cpu"`) {
		t.Error("no volvió a mostrarse")
	}

	// Lo inválido no se guarda.
	for name, f := range map[string]url.Values{
		"host desconocido": {"host": {"otro"}, "item": {"tile:cpu"}, "hidden": {"1"}},
		"item inválido":    {"host": {"vm"}, "item": {"<script>"}, "hidden": {"1"}},
		"hidden inválido":  {"host": {"vm"}, "item": {"tile:cpu"}, "hidden": {"sí"}},
	} {
		if code := post("/hardware/hidden", f); code != http.StatusBadRequest {
			t.Errorf("%s = %d", name, code)
		}
	}
	if code := post("/hardware/order", url.Values{"host": {"vm"}}); code != http.StatusBadRequest {
		t.Errorf("orden vacío = %d", code)
	}
}
