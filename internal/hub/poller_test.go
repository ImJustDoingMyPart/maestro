package hub

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"slices"
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
	sys *agent.System
}

func (f *fakeAgent) Containers(context.Context) ([]agent.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cs, f.err
}

func (f *fakeAgent) Logs(context.Context, string, agent.LogOptions) (iter.Seq2[agent.LogLine, error], error) {
	return nil, errors.New("fakeAgent no tiene logs")
}

func (f *fakeAgent) StackFiles(context.Context, string) (agent.StackFiles, error) {
	return agent.StackFiles{}, errors.New("fakeAgent no tiene stacks")
}

func (f *fakeAgent) CheckStackFile(context.Context, string, string, string) (agent.CheckResult, error) {
	return agent.CheckResult{}, errors.New("fakeAgent no tiene stacks")
}

func (f *fakeAgent) WriteStackFile(context.Context, string, string, string, string) (agent.WriteResult, error) {
	return agent.WriteResult{}, errors.New("fakeAgent no tiene stacks")
}

func (f *fakeAgent) StackAction(context.Context, string, string) (iter.Seq2[agent.ActionEvent, error], error) {
	return nil, errors.New("fakeAgent no tiene stacks")
}

func (f *fakeAgent) System(context.Context) (agent.System, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sys == nil {
		return agent.System{}, errors.New("fakeAgent sin colector")
	}
	return *f.sys, nil
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
	p := newPoller(map[string]agentClient{"vm": vm, "lxc": lxc}, nil, slog.New(slog.DiscardHandler))
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
	if got := snapState(snap.Services[0].ID, snap); got != StateUp {
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
	if got := snapState(snap.Services[0].ID, snap); got != StateUnknown {
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
	if got := snapState(snap.Services[0].ID, snap); got != StateDown {
		t.Errorf("estado = %s, quería down", got)
	}
}

func TestPollerSobreviveAUnPanico(t *testing.T) {
	p := newPoller(map[string]agentClient{"vm": panicAgent{&fakeAgent{}}}, nil, slog.New(slog.DiscardHandler))
	p.safePoll(context.Background()) // no tiene que propagar el pánico
	snap := p.Snapshot()
	if snap.HostOK("vm") || len(snap.Hosts) != 1 || !strings.Contains(snap.Hosts[0].Err, "boom") {
		t.Errorf("el pánico no quedó como error del host: %+v", snap.Hosts)
	}
}

// panicAgent toma de fakeAgent los métodos que no redefine.
type panicAgent struct{ *fakeAgent }

func (panicAgent) Containers(context.Context) ([]agent.Container, error) { panic("boom") }

func (panicAgent) Logs(context.Context, string, agent.LogOptions) (iter.Seq2[agent.LogLine, error], error) {
	panic("boom")
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

// Una unidad declarada en el archivo (host y unit) que también se muestra
// desde la bandeja no se suma dos veces: la tarjeta es la del archivo, y
// la bandeja la marca como suya. "nginx" es "nginx.service".
func TestPollerUnidadDelArchivoNoSeRepite(t *testing.T) {
	vm := &fakeAgent{sys: &agent.System{Units: []agent.Unit{
		{Name: "restic-backup.timer", Active: "active"},
		{Name: "nginx.service", Active: "active"},
		{Name: "otra.service", Active: "active"},
	}}}
	file := map[string]catalog.FileService{
		"restic": {Name: "Backups", Host: "vm", Unit: "restic-backup.timer"},
		"nginx":  {Host: "vm", Unit: "nginx"},
	}
	p := newPoller(map[string]agentClient{"vm": vm}, file, slog.New(slog.DiscardHandler))
	p.SetShown(map[string]bool{
		"found:vm/timer/restic-backup.timer": true,
		"found:vm/unit/otra.service":         true,
	})
	p.poll(context.Background())
	snap := p.Snapshot()

	var ids []string
	for _, s := range snap.Services {
		ids = append(ids, s.ID)
	}
	slices.Sort(ids)
	if want := []string{"found:vm/unit/otra.service", "nginx", "restic"}; !slices.Equal(ids, want) {
		t.Errorf("servicios = %v, want %v", ids, want)
	}
	in := map[string]string{}
	for _, d := range snap.Discovered {
		in[d.Key()] = d.InFile
	}
	if in["found:vm/timer/restic-backup.timer"] != "restic" || in["found:vm/unit/nginx.service"] != "nginx" || in["found:vm/unit/otra.service"] != "" {
		t.Errorf("InFile = %v", in)
	}
	shown := map[string]bool{"found:vm/unit/otra.service": true}
	if n := pending(snap.Discovered, shown); n != 0 {
		t.Errorf("pending = %d: lo del archivo no queda en la bandeja", n)
	}
	rows := foundRows(snap.Discovered, shown)
	for _, r := range rows[0].Rows {
		if r.Name == "restic-backup.timer" && (!r.Shown || r.Href != servicePath("restic")) {
			t.Errorf("fila del timer = %+v", r)
		}
	}
}
