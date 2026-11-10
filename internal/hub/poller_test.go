package hub

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
)

// fakeAgent devuelve lo que se le cargue; se puede cambiar entre vueltas.
type fakeAgent struct {
	mu  sync.Mutex
	cs  []agent.Container
	err error
}

func (f *fakeAgent) Containers(context.Context) ([]agent.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cs, f.err
}

func (f *fakeAgent) set(cs []agent.Container, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cs, f.err = cs, err
}

func composeContainer(name, project, service, state, health string) agent.Container {
	return agent.Container{
		ID: "id-" + name, Name: name, Image: "img/" + service, State: state, Health: health,
		Labels: map[string]string{
			"com.docker.compose.project": project,
			"com.docker.compose.service": service,
		},
	}
}

func TestPollerAgentQueFallaYVuelve(t *testing.T) {
	vm := &fakeAgent{cs: []agent.Container{composeContainer("jellyfin", "media", "jellyfin", "running", "healthy")}}
	lxc := &fakeAgent{err: errors.New("connection refused")}
	p := newPoller(map[string]containerLister{"vm": vm, "lxc": lxc}, nil, slog.New(slog.DiscardHandler))
	t0 := time.Unix(1_800_000_000, 0)
	p.now = func() time.Time { return t0 }

	p.poll(context.Background())
	snap := p.Snapshot()
	if len(snap.Services) != 1 || snap.Services[0].ID != "media/jellyfin" || snap.Services[0].Host != "vm" {
		t.Fatalf("servicios = %+v", snap.Services)
	}
	if !snap.HostOK("vm") || snap.HostOK("lxc") {
		t.Errorf("hosts = %+v", snap.Hosts)
	}
	if got := serviceState(snap.Services[0], snap); got != StateUp {
		t.Errorf("estado = %s, quería up", got)
	}

	// El vm deja de responder: el servicio sigue en la lista (con los datos
	// viejos) pero sin datos en el semáforo.
	vm.set(nil, errors.New("timeout"))
	p.now = func() time.Time { return t0.Add(15 * time.Second) }
	p.poll(context.Background())
	snap = p.Snapshot()
	if len(snap.Services) != 1 {
		t.Fatalf("el servicio desapareció al caerse el agent: %+v", snap.Services)
	}
	if got := serviceState(snap.Services[0], snap); got != StateUnknown {
		t.Errorf("estado con el agent caído = %s, quería unknown", got)
	}
	for _, h := range snap.Hosts {
		if h.Name == "vm" && (h.OK || h.Err != "timeout" || !h.LastOK.Equal(t0)) {
			t.Errorf("estado del host vm = %+v", h)
		}
	}

	// Vuelve.
	vm.set([]agent.Container{composeContainer("jellyfin", "media", "jellyfin", "exited", "")}, nil)
	p.poll(context.Background())
	snap = p.Snapshot()
	if !snap.HostOK("vm") {
		t.Error("vm sigue caído después de responder")
	}
	if got := serviceState(snap.Services[0], snap); got != StateDown {
		t.Errorf("estado = %s, quería down", got)
	}
}

func TestPollerSobreviveAUnPanico(t *testing.T) {
	p := newPoller(map[string]containerLister{"vm": panicAgent{}}, nil, slog.New(slog.DiscardHandler))
	p.safePoll(context.Background()) // no tiene que propagar el pánico
	snap := p.Snapshot()
	if snap.HostOK("vm") || len(snap.Hosts) != 1 || !strings.Contains(snap.Hosts[0].Err, "boom") {
		t.Errorf("el pánico no quedó como error del host: %+v", snap.Hosts)
	}
}

type panicAgent struct{}

func (panicAgent) Containers(context.Context) ([]agent.Container, error) { panic("boom") }

func TestServiceState(t *testing.T) {
	snap := Snapshot{Hosts: []HostStatus{{Name: "vm", OK: true}, {Name: "lxc", OK: false}}}
	c := func(state, health string) catalog.Container {
		return catalog.Container{State: state, Health: health}
	}
	svc := func(src catalog.Source, host string, cs ...catalog.Container) catalog.Service {
		return catalog.Service{Source: src, Host: host, Containers: cs}
	}
	tests := []struct {
		name string
		svc  catalog.Service
		want State
	}{
		{"corriendo sin healthcheck", svc(catalog.SourceCompose, "vm", c("running", "")), StateUp},
		{"corriendo y sano", svc(catalog.SourceCompose, "vm", c("running", "healthy")), StateUp},
		{"corriendo pero enfermo", svc(catalog.SourceCompose, "vm", c("running", "unhealthy")), StateDown},
		{"arrancando", svc(catalog.SourceCompose, "vm", c("running", "starting")), StateUnknown},
		{"detenido", svc(catalog.SourceCompose, "vm", c("exited", "")), StateDown},
		{"reiniciando", svc(catalog.SourceCompose, "vm", c("restarting", "")), StateDown},
		{"una réplica caída", svc(catalog.SourceCompose, "vm", c("running", ""), c("exited", "")), StateDown},
		{"contenedor suelto", svc(catalog.SourceContainer, "vm", c("running", "")), StateUp},
		{"host sin respuesta", svc(catalog.SourceCompose, "lxc", c("running", "")), StateUnknown},
		{"systemd todavía sin datos", svc(catalog.SourceSystemd, "vm"), StateUnknown},
		{"externo todavía sin datos", svc(catalog.SourceExternal, ""), StateUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := serviceState(tt.svc, snap); got != tt.want {
				t.Errorf("serviceState = %s, quería %s", got, tt.want)
			}
		})
	}
}

func TestShortImage(t *testing.T) {
	for in, want := range map[string]string{
		"jellyfin/jellyfin:latest":                                      "jellyfin/jellyfin:latest",
		"docker.io/valkey/valkey:9@sha256:c123e3715db63d06d4ad6964884":  "docker.io/valkey/valkey:9",
		"sha256:860aeb5d63ce509508f6dcfb2bc8a1c1dd2bc60555b562b45ce90e": "sha256:860aeb5d63ce",
		"sha256:corto": "sha256:corto",
	} {
		if got := shortImage(in); got != want {
			t.Errorf("shortImage(%q) = %q, quería %q", in, got, want)
		}
	}
}
