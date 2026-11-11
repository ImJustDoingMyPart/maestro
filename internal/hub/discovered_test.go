package hub

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBandejaDeDescubiertos(t *testing.T) {
	snap := Snapshot{
		At:    time.Now(),
		Hosts: []HostStatus{{Name: "vm", OK: true}},
		Discovered: []Discovered{
			{Host: "adguard", Kind: "unit", Name: "AdGuardHome.service", State: StateUp, Detail: "activa"},
			{Host: "pve", Kind: "lxc", Name: "102 adguard", State: StateUp},
			{Host: "vm", Kind: "timer", Name: "restic-backup.timer", State: StateDown, Detail: "última corrida falló"},
		},
	}
	srv := testServer(t, snap)
	c := loggedIn(t, srv)
	get := func(path string) string {
		t.Helper()
		res, _ := c.Get(srv.URL + path)
		return body(t, res)
	}

	if b := get("/"); !strings.Contains(b, "3 descubiertos") || strings.Contains(b, "AdGuardHome.service") {
		t.Error("la portada tiene que mostrar el chip y ninguna tarjeta")
	}
	b := get("/discovered")
	for _, want := range []string{"AdGuardHome.service", "unidad de systemd en adguard · activa", "LXC en pve", "timer de systemd en vm", `value="found:adguard/unit/AdGuardHome.service"`} {
		if !strings.Contains(b, want) {
			t.Errorf("la bandeja no tiene %q", want)
		}
	}

	res := postForm(t, c, srv.URL+"/discovered/show", url.Values{"key": {"found:vm/timer/restic-backup.timer"}}, srv.URL)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/discovered" {
		t.Fatalf("mostrar = %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	b = get("/")
	if !strings.Contains(b, "2 descubiertos") || !strings.Contains(b, `data-key="found:vm/timer/restic-backup.timer"`) ||
		!strings.Contains(b, "Volver a la bandeja") || !strings.Contains(b, `status down`) {
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
	if b := get("/"); !strings.Contains(b, ">Backups<") || !strings.Contains(b, `data-group="Sistema"`) {
		t.Error("renombrar o mover un descubierto no anduvo")
	}

	res = postForm(t, c, srv.URL+"/discovered/unshow", url.Values{"key": {"found:vm/timer/restic-backup.timer"}}, srv.URL)
	res.Body.Close()
	if b := get("/"); strings.Contains(b, "found:vm/timer") || !strings.Contains(b, "3 descubiertos") {
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
