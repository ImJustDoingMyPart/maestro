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
		_, b := getPage(t, c, srv.URL+"/hardware?host="+host)
		return b
	}

	if b := page("vm"); !strings.Contains(b, `"Live":true`) || strings.Index(b, `"ID":"tile:cpu"`) > strings.Index(b, `"ID":"tile:ram"`) {
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
	if strings.Index(b, `"ID":"tile:ram"`) > strings.Index(b, `"ID":"tile:cpu"`) {
		t.Error("no tomó el orden")
	}
	for _, want := range []string{`"ID":"tile:cpu","Hidden":true`, `"ID":"chart:temp","Hidden":true`} {
		if !strings.Contains(b, want) {
			t.Errorf("falta %q", want)
		}
	}
	if strings.Contains(page("pve"), `"Hidden":true`) {
		t.Error("lo oculto en vm se ocultó en pve")
	}

	// Mostrar de nuevo.
	post("/hardware/hidden", url.Values{"host": {"vm"}, "item": {"tile:cpu"}, "hidden": {"0"}})
	if strings.Contains(page("vm"), `"ID":"tile:cpu","Hidden":true`) {
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

func TestOrdenDeHostsEnHardware(t *testing.T) {
	m := &agent.HostMetrics{CPU: 5, MemTotal: 1 << 30}
	snap := Snapshot{
		Hosts:   []HostStatus{{Name: "adguard", OK: true}, {Name: "pve", OK: true}, {Name: "vm", OK: true}},
		Systems: map[string]agent.System{"adguard": {Metrics: m}, "pve": {Metrics: m}, "vm": {Metrics: m}},
	}
	srv := testServer(t, snap)
	c := loggedIn(t, srv)
	page := func() string {
		t.Helper()
		_, b := getPage(t, c, srv.URL+"/hardware")
		return b
	}
	post := func(hosts ...string) int {
		t.Helper()
		res := postForm(t, c, srv.URL+"/hardware/hosts", url.Values{"host": hosts}, srv.URL)
		res.Body.Close()
		return res.StatusCode
	}
	if b := page(); !strings.Contains(b, `"HWHost":"adguard"`) {
		t.Fatal("sin orden, tenía que mostrar el primero")
	}
	// Un orden parcial: los que faltan van al final, en su orden.
	if code := post("vm"); code != http.StatusNoContent {
		t.Fatalf("ordenar = %d", code)
	}
	b := page()
	if !strings.Contains(b, `"HWHost":"vm"`) || strings.Index(b, `"Name":"vm","OK"`) > strings.Index(b, `"Name":"adguard","OK"`) ||
		strings.Index(b, `"Name":"adguard","OK"`) > strings.Index(b, `"Name":"pve","OK"`) {
		t.Error("el primero del orden no es el primero ni el que se muestra")
	}
	// Con ?host= se ve el pedido.
	if _, b2 := getPage(t, c, srv.URL+"/hardware?host=pve"); !strings.Contains(b2, `"HWHost":"pve"`) {
		t.Error("?host= no manda")
	}
	if code := post("pve", "adguard", "vm"); code != http.StatusNoContent {
		t.Fatalf("ordenar = %d", code)
	}
	if b := page(); !strings.Contains(b, `"HWHost":"pve"`) {
		t.Error("no tomó el orden nuevo")
	}
	for name, hosts := range map[string][]string{"vacío": nil, "desconocido": {"otro"}, "repetido": {"vm", "vm"}} {
		if code := post(hosts...); code != http.StatusBadRequest {
			t.Errorf("%s = %d", name, code)
		}
	}
}

func TestSeccionesDeHardware(t *testing.T) {
	temp := 40.0
	m := &agent.HostMetrics{CPU: 5, MemTotal: 1 << 30, FS: []agent.FSUsage{{Mount: "/", Total: 10, Used: 5}, {Mount: "/mnt/mis datos", Total: 10, Used: 1}}}
	snap := Snapshot{
		Hosts: []HostStatus{{Name: "pve", OK: true}},
		Systems: map[string]agent.System{"pve": {Metrics: m,
			SMART:  []agent.SMARTDisk{{Device: "/dev/sda", TempC: &temp}},
			Guests: []agent.Guest{{Kind: "lxc", VMID: 102, Name: "dns", Status: "running", MaxMem: 1 << 30}}}},
	}
	srv := testServer(t, snap)
	c := loggedIn(t, srv)
	page := func() string {
		t.Helper()
		_, b := getPage(t, c, srv.URL+"/hardware")
		return b
	}
	post := func(path string, form url.Values) {
		t.Helper()
		form.Set("host", "pve")
		res := postForm(t, c, srv.URL+path, form, srv.URL)
		res.Body.Close()
		if res.StatusCode != http.StatusNoContent {
			t.Fatalf("%s %v = %d", path, form, res.StatusCode)
		}
	}
	order := func(b string, items ...string) bool {
		last := -1
		for _, it := range items {
			i := strings.Index(b, `"ID":"`+it+`"`)
			if i < last {
				return false
			}
			last = i
		}
		return true
	}
	b := page()
	if !order(b, "section:storage", "section:tiles", "section:charts", "section:guests") {
		t.Error("almacenamiento no va primero")
	}
	if !order(b, "fs:/", "fs:/mnt/mis_datos", "disk:/dev/sda") || !strings.Contains(b, `"ID":"guest:102"`) {
		t.Error("tarjetas sin clave del modo edición")
	}
	post("/hardware/order", url.Values{"item": {"section:guests", "section:tiles"}})
	post("/hardware/order", url.Values{"item": {"disk:/dev/sda", "fs:/mnt/mis_datos", "fs:/"}})
	post("/hardware/hidden", url.Values{"item": {"guest:102"}, "hidden": {"1"}})
	b = page()
	if !order(b, "section:guests", "section:tiles", "section:storage") {
		t.Error("no tomó el orden de las secciones")
	}
	if !order(b, "disk:/dev/sda", "fs:/mnt/mis_datos", "fs:/") {
		t.Error("no tomó el orden del almacenamiento")
	}
	if !strings.Contains(b, `"ID":"guest:102","Hidden":true`) {
		t.Error("no ocultó la LXC")
	}
}
