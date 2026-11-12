package hub

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

func TestCambiarIcono(t *testing.T) {
	now := time.Now()
	snap := Snapshot{
		At:      now,
		Hosts:   []HostStatus{{Name: "pve", OK: true}},
		Systems: map[string]agent.System{"pve": {Guests: []agent.Guest{{VMID: 102, Kind: "lxc", Name: "dns", Status: "running", MaxMem: 1 << 30}}}},
	}
	for _, h := range snap.Hosts {
		snap.Discovered = append(snap.Discovered, discoveredFrom(h.Name, snap.Systems[h.Name], true, now)...)
	}
	srv := testServer(t, snap)
	c := loggedIn(t, srv)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	set := func(icon, back string) *http.Response {
		t.Helper()
		res := postForm(t, c, srv.URL+"/icons/set", url.Values{"key": {"found:pve/lxc/102"}, "icon": {icon}, "back": {back}}, srv.URL)
		res.Body.Close()
		return res
	}
	if res := set("router", "/discovered"); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/discovered" {
		t.Fatalf("guardar = %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// Sin resolver de logos en el test, "router" queda como Material Symbol.
	res, _ := c.Get(srv.URL + "/discovered")
	if b := body(t, res); !strings.Contains(b, `<i>router</i>`) || !strings.Contains(b, `data-icon="router"`) {
		t.Error("la bandeja no usa el ícono elegido")
	}
	res, _ = c.Get(srv.URL + "/hardware?host=pve")
	if b := body(t, res); !strings.Contains(b, `<i>router</i>`) {
		t.Error("la tarjeta de la LXC no usa el ícono elegido")
	}

	// Volver solo a una ruta propia.
	if res := set("router", "https://otro.example/"); res.Header.Get("Location") != "/" {
		t.Errorf("volvió a %q", res.Header.Get("Location"))
	}
	if res := set("<script>", "/"); res.StatusCode != http.StatusBadRequest {
		t.Errorf("ícono inválido = %d", res.StatusCode)
	}
	// Vacío vuelve al de siempre.
	set("", "/")
	res, _ = c.Get(srv.URL + "/discovered")
	if b := body(t, res); strings.Contains(b, `<i>router</i>`) {
		t.Error("vacío no sacó el ícono elegido")
	}
}
