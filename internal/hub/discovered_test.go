package hub

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

func TestBandejaDeDescubiertos(t *testing.T) {
	now := time.Now()
	snap := Snapshot{
		At:    now,
		Hosts: []HostStatus{{Name: "adguard", OK: true}, {Name: "pve", OK: true}, {Name: "vm", OK: true}},
		Systems: map[string]agent.System{
			"adguard": {Units: []agent.Unit{{Name: "AdGuardHome.service", Description: "AdGuard Home", Active: "active", Sub: "running"}}},
			"pve":     {Units: []agent.Unit{}, Guests: []agent.Guest{{VMID: 102, Kind: "lxc", Name: "dns", Status: "running", CPUs: 1, MaxMem: 1 << 30}}},
			"vm": {Units: []agent.Unit{{Name: "restic-backup.timer", Active: "active", Sub: "waiting", Result: "exit-code", ExitStatus: 1,
				LastRun: now.Add(-3 * time.Hour), Triggers: "restic-backup.service"}}},
		},
	}
	for _, h := range snap.Hosts {
		snap.Discovered = append(snap.Discovered, discoveredFrom(h.Name, snap.Systems[h.Name], true, now)...)
	}
	srv := testServer(t, snap)
	c := loggedIn(t, srv)
	get := func(path string) string {
		t.Helper()
		res, _ := c.Get(srv.URL + path)
		return body(t, res)
	}

	if b := get("/clasica"); !strings.Contains(b, "3 descubiertos") || strings.Contains(b, "AdGuardHome.service") {
		t.Error("la portada tiene que mostrar el chip y ninguna tarjeta")
	}
	b := get("/discovered")
	for _, want := range []string{"AdGuardHome.service", "unidad de systemd · AdGuard Home", "dns", "LXC 102 · 1,0 GB de RAM · 1 núcleo",
		"timer de systemd · última corrida falló (exit 1)", `value="found:adguard/unit/AdGuardHome.service"`, `value="found:pve/lxc/102"`, `<h2 class="section-title">vm <span`} {
		if !strings.Contains(b, want) {
			t.Errorf("la bandeja no tiene %q", want)
		}
	}

	res := postForm(t, c, srv.URL+"/discovered/show", url.Values{"key": {"found:vm/timer/restic-backup.timer"}}, srv.URL)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/discovered" {
		t.Fatalf("mostrar = %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	b = get("/clasica")
	if !strings.Contains(b, "2 descubiertos") || !strings.Contains(b, `data-key="found:vm/timer/restic-backup.timer"`) ||
		!strings.Contains(b, "Volver a la bandeja") || !strings.Contains(b, `state-pill down`) {
		t.Errorf("después de mostrar, la portada:\n%.3000s", b)
	}
	if b := get("/discovered"); !strings.Contains(b, "Volver a la bandeja") {
		t.Error("la bandeja no marca el mostrado")
	}
	// Se puede renombrar y mover como cualquier tarjeta.
	res = postForm(t, c, srv.URL+"/names", url.Values{"key": {"found:vm/timer/restic-backup.timer"}, "name": {"Backups"}}, srv.URL)
	res.Body.Close()
	res = postForm(t, c, srv.URL+"/groups/move", url.Values{"key": {"found:vm/timer/restic-backup.timer"}, "group": {"Sistema"}}, srv.URL)
	res.Body.Close()
	if b := get("/clasica"); !strings.Contains(b, ">Backups<") || !strings.Contains(b, `data-group="Sistema"`) {
		t.Error("renombrar o mover un descubierto no anduvo")
	}

	res = postForm(t, c, srv.URL+"/discovered/unshow", url.Values{"key": {"found:vm/timer/restic-backup.timer"}}, srv.URL)
	res.Body.Close()
	if b := get("/clasica"); strings.Contains(b, "found:vm/timer") || !strings.Contains(b, "3 descubiertos") {
		t.Error("volver a la bandeja no lo sacó de la portada")
	}
	for _, key := range []string{"service:a/b", "found:solo", "found:a/b"} {
		res = postForm(t, c, srv.URL+"/discovered/show", url.Values{"key": {key}}, srv.URL)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("clave %q = %d", key, res.StatusCode)
		}
	}
}

func TestPaginaDeUnDescubierto(t *testing.T) {
	now := time.Now()
	snap := Snapshot{
		At:      now,
		Hosts:   []HostStatus{{Name: "vm", OK: true}},
		Systems: map[string]agent.System{"vm": {Units: []agent.Unit{{Name: "netbird.service", Active: "failed", Result: "signal"}}}},
	}
	snap.Discovered = discoveredFrom("vm", snap.Systems["vm"], true, now)
	srv := testServer(t, snap)
	c := loggedIn(t, srv)
	res, _ := c.Get(srv.URL + "/services/found:vm/unit/netbird.service")
	b := body(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(b, "netbird.service falló (signal)") || !strings.Contains(b, "systemd · netbird.service") {
		t.Errorf("página = %d\n%.2000s", res.StatusCode, b)
	}
}
