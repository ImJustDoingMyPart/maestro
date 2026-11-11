package hub

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"maps"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/config"
)

// agentClient es lo que el hub necesita de un agent. Lo cumple
// *agent.Client; los tests pasan uno falso.
type agentClient interface {
	Containers(ctx context.Context) ([]agent.Container, error)
	Logs(ctx context.Context, id string, opts agent.LogOptions) (iter.Seq2[agent.LogLine, error], error)
	StackFiles(ctx context.Context, project string) (agent.StackFiles, error)
	CheckStackFile(ctx context.Context, project, name, content string) (agent.CheckResult, error)
	WriteStackFile(ctx context.Context, project, name, content, hash string) (agent.WriteResult, error)
	StackAction(ctx context.Context, project, action string) (iter.Seq2[agent.ActionEvent, error], error)
	System(ctx context.Context) (agent.System, error)
}

// HostStatus es cómo le fue al último pedido a un agent.
type HostStatus struct {
	Name   string
	OK     bool
	Err    string    // vacío si OK
	LastOK time.Time // cero si nunca respondió
}

// Discovered es algo que un agent encontró y que no va solo a la portada:
// una unidad o un timer de systemd, una LXC o una VM de Proxmox. Va a la
// bandeja de Descubiertos, de donde "Mostrar" lo pasa a la portada. Lo
// llenan los agents desde la etapa 7; hasta entonces la lista está vacía.
type Discovered struct {
	Host   string
	Kind   string // "unit", "timer", "lxc" o "vm"
	Name   string // nombre de la unidad o del timer, o VMID de la LXC o VM
	Label  string // lo que se muestra: el nombre de la unidad o de la LXC o VM
	State  State  // con la misma regla que el monitor (status.Evaluate)
	Reason string // motivo del estado; vacío si está arriba
	Detail string // descripción de la unidad, "corrió hace 3 horas", …
}

// Key es la clave de la tarjeta: "found:<host>/<tipo>/<nombre>".
func (d Discovered) Key() string { return "found:" + d.Host + "/" + d.Kind + "/" + d.Name }

// Snapshot es la foto que muestra la portada.
type Snapshot struct {
	Services   []catalog.Service
	Discovered []Discovered
	Hosts      []HostStatus // ordenados por nombre
	// Systems es la última muestra de cada host que tiene colector (los
	// agents de antes de la etapa 7 no tienen). No se modifica: cada foto
	// trae un mapa nuevo.
	Systems map[string]agent.System
	At      time.Time // cero hasta la primera vuelta
}

// HostOK dice si el agent de host respondió en la última vuelta.
func (s Snapshot) HostOK(host string) bool {
	for _, h := range s.Hosts {
		if h.Name == host {
			return h.OK
		}
	}
	return false
}

type hostState struct {
	status     HostStatus
	containers []agent.Container // los últimos que se conocen, aunque el agent haya dejado de responder
	system     *agent.System     // la última muestra; nil si el agent no tiene colector
}

// Poller consulta a los agents cada intervalo y arma la lista de servicios.
type Poller struct {
	agents map[string]agentClient
	file   map[string]catalog.FileService
	ui     map[string]catalog.FileService // externos de la interfaz; protegido por mu
	shown  map[string]bool                // descubiertos que se muestran; protegido por mu
	// layout es lo del archivo que solo usa la portada (stacks: y
	// groups:). No cambia después de NewPoller.
	layout   fileLayout
	catalog  *catalog.Catalog
	interval time.Duration
	timeout  time.Duration
	logger   *slog.Logger
	now      func() time.Time
	// onPoll recibe cada foto nueva (el monitor). Se fija antes de Run.
	onPoll func(Snapshot)

	mu    sync.RWMutex
	hosts map[string]*hostState
	snap  Snapshot
}

// NewPoller arma un poller con un cliente por host de la config.
func NewPoller(cfg *config.Config, logger *slog.Logger) *Poller {
	agents := make(map[string]agentClient, len(cfg.Hosts))
	for name, h := range cfg.Hosts {
		agents[name] = agent.NewClient(h.Agent, h.Token)
	}
	p := newPoller(agents, cfg.FileServices(), logger)
	p.layout = fileLayout{Stacks: cfg.Stacks, Groups: cfg.Groups}
	return p
}

// fileLayout es lo de maestro.yaml que solo usa la portada.
type fileLayout struct {
	Stacks map[string]config.Stack // por "<host>/<proyecto>"
	Groups []string                // orden de los grupos; vacío si no lo fija
}

// FileLayout da las secciones stacks: y groups: del archivo.
func (p *Poller) FileLayout() fileLayout { return p.layout }

func newPoller(agents map[string]agentClient, file map[string]catalog.FileService, logger *slog.Logger) *Poller {
	p := &Poller{
		agents:   agents,
		file:     file,
		catalog:  catalog.New(logger),
		interval: 15 * time.Second,
		timeout:  10 * time.Second,
		logger:   logger,
		now:      time.Now,
		hosts:    make(map[string]*hostState, len(agents)),
	}
	for name := range agents {
		p.hosts[name] = &hostState{status: HostStatus{Name: name}}
	}
	return p
}

// Logs abre los logs de un contenedor pidiéndoselos al agent de su host.
func (p *Poller) Logs(ctx context.Context, host, containerID string, opts agent.LogOptions) (iter.Seq2[agent.LogLine, error], error) {
	a, ok := p.agents[host] // el mapa no cambia después de newPoller: se lee sin lock
	if !ok {
		return nil, fmt.Errorf("el host %q no tiene agent", host)
	}
	return a.Logs(ctx, containerID, opts)
}

// errNoAgent es un host que no está en la config.
var errNoAgent = errors.New("el host no tiene agent")

func (p *Poller) agent(host string) (agentClient, error) {
	a, ok := p.agents[host]
	if !ok {
		return nil, fmt.Errorf("%w: %q", errNoAgent, host)
	}
	return a, nil
}

// StackFiles lee los archivos de un stack del host.
func (p *Poller) StackFiles(ctx context.Context, host, project string) (agent.StackFiles, error) {
	a, err := p.agent(host)
	if err != nil {
		return agent.StackFiles{}, err
	}
	return a.StackFiles(ctx, project)
}

// CheckStackFile valida un archivo de un stack del host sin escribirlo.
func (p *Poller) CheckStackFile(ctx context.Context, host, project, name, content string) (agent.CheckResult, error) {
	a, err := p.agent(host)
	if err != nil {
		return agent.CheckResult{}, err
	}
	return a.CheckStackFile(ctx, project, name, content)
}

// WriteStackFile escribe un archivo de un stack del host.
func (p *Poller) WriteStackFile(ctx context.Context, host, project, name, content, hash string) (agent.WriteResult, error) {
	a, err := p.agent(host)
	if err != nil {
		return agent.WriteResult{}, err
	}
	return a.WriteStackFile(ctx, project, name, content, hash)
}

// StackAction corre una acción de compose en un stack del host.
func (p *Poller) StackAction(ctx context.Context, host, project, action string) (iter.Seq2[agent.ActionEvent, error], error) {
	a, err := p.agent(host)
	if err != nil {
		return nil, err
	}
	return a.StackAction(ctx, project, action)
}

// Snapshot devuelve la última foto. Se puede llamar desde cualquier goroutine.
func (p *Poller) Snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.snap
}

// Run consulta enseguida y después cada intervalo, hasta que se cancele ctx.
func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		p.safePoll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// safePoll corre una vuelta con recover: un pánico en una vuelta se registra
// y la siguiente vuelve a intentar, en vez de tirar abajo el hub entero.
func (p *Poller) safePoll(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("pánico en el poller", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	p.poll(ctx)
	if p.onPoll != nil {
		p.onPoll(p.Snapshot())
	}
}

// poll pide los contenedores a todos los agents a la vez y rearma la foto.
func (p *Poller) poll(ctx context.Context) {
	type result struct {
		host   string
		cs     []agent.Container
		err    error
		sys    *agent.System
		sysErr error
	}
	results := make(chan result, len(p.agents))

	// Una goroutine por agent: uno lento no demora a los demás. El canal
	// tiene lugar para todos los resultados, así ninguna queda bloqueada.
	for name, a := range p.agents {
		go func() {
			// recover solo atrapa pánicos de su propia goroutine: el de
			// safePoll no ve los de acá, y un pánico sin atrapar en
			// cualquier goroutine termina el proceso. Por eso cada una
			// tiene el suyo y lo convierte en un error de ese host.
			var res result
			res.host = name
			defer func() {
				if r := recover(); r != nil {
					p.logger.Error("pánico al consultar un agent", "host", name, "panic", r, "stack", string(debug.Stack()))
					res.err = fmt.Errorf("pánico: %v", r)
				}
				results <- res
			}()
			ctx, cancel := context.WithTimeout(ctx, p.timeout)
			defer cancel()
			res.cs, res.err = a.Containers(ctx)
			if res.err != nil {
				return
			}
			sys, err := a.System(ctx)
			res.sysErr = err
			if err == nil {
				res.sys = &sys
			}
		}()
	}

	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	for range p.agents {
		r := <-results
		h := p.hosts[r.host]
		wasFailing := h.status.Err != ""
		if r.err != nil {
			// Se avisa al cambiar de estado, no en cada vuelta.
			if !wasFailing {
				p.logger.Warn("agent sin respuesta", "host", r.host, "err", r.err)
			}
			h.status.OK, h.status.Err = false, r.err.Error()
			continue // se quedan los contenedores de la vuelta anterior
		}
		if wasFailing {
			p.logger.Info("agent responde de nuevo", "host", r.host)
		}
		h.status = HostStatus{Name: r.host, OK: true, LastOK: now}
		h.containers = r.cs
		switch {
		case r.sys != nil:
			h.system = r.sys
		case isNotImplemented(r.sysErr):
			h.system = nil // un agent viejo, sin /v1/system
		default:
			p.logger.Debug("muestra del host", "host", r.host, "err", r.sysErr)
		}
	}

	p.rebuildLocked(now)
}

// rebuildLocked rearma la foto con los últimos contenedores de cada host,
// sin pedirles nada a los agents. Se llama con mu tomado.
func (p *Poller) rebuildLocked(at time.Time) {
	var all []catalog.Container
	var found []Discovered
	hosts := make([]HostStatus, 0, len(p.hosts))
	systems := map[string]agent.System{}
	for _, name := range slices.Sorted(maps.Keys(p.hosts)) {
		h := p.hosts[name]
		hosts = append(hosts, h.status)
		if h.system != nil {
			systems[name] = *h.system
			found = append(found, discoveredFrom(name, *h.system, h.status.OK, at)...)
		}
		for _, c := range h.containers {
			all = append(all, catalog.Container{
				Host: name, ID: c.ID, Name: c.Name, Image: c.Image,
				State: c.State, Health: c.Health, Restarts: c.Restarts,
				ExitCode: c.ExitCode, StartedAt: c.StartedAt, Labels: c.Labels,
				LastExitCode: c.LastExitCode, LastExitAt: c.LastExitAt,
			})
		}
	}
	file := p.file
	if len(p.ui) > 0 {
		// El archivo gana: un externo de la interfaz con una clave que
		// también está en el archivo no cuenta.
		file = maps.Clone(p.file)
		if file == nil {
			file = map[string]catalog.FileService{}
		}
		for k, fs := range p.ui {
			if _, ok := file[k]; !ok {
				fs.FromUI = true
				file[k] = fs
			}
		}
	}
	services := p.catalog.Build(all, file)
	for _, d := range found {
		if p.shown[d.Key()] {
			services = append(services, foundService(d))
		}
	}
	p.snap = Snapshot{Services: services, Discovered: found, Hosts: hosts, Systems: systems, At: at}
}

// SetShown cambia qué descubiertos se muestran (pasan a ser servicios) y
// rearma la foto enseguida, como SetUIServices.
func (p *Poller) SetShown(shown map[string]bool) {
	p.mu.Lock()
	p.shown = shown
	p.rebuildLocked(p.snap.At)
	snap := p.snap
	p.mu.Unlock()
	if p.onPoll != nil {
		p.onPoll(snap)
	}
}

// isNotImplemented dice si el agent contestó que no tiene la ruta (404 de
// un agent viejo, 501 de uno sin colector).
func isNotImplemented(err error) bool {
	return agent.IsStatus(err, http.StatusNotFound) || agent.IsStatus(err, http.StatusNotImplemented)
}

// SetUIServices cambia los servicios externos creados desde la interfaz y
// rearma la foto enseguida (también el monitor, que arranca o para sus
// checks), sin esperar la próxima vuelta.
func (p *Poller) SetUIServices(ui map[string]catalog.FileService) {
	p.mu.Lock()
	p.ui = ui
	p.rebuildLocked(p.snap.At)
	snap := p.snap
	p.mu.Unlock()
	if p.onPoll != nil {
		p.onPoll(snap)
	}
}

// FileKeys dice qué claves de servicio declara el archivo.
func (p *Poller) FileKeys() map[string]bool {
	out := make(map[string]bool, len(p.file))
	for k := range p.file { // p.file no cambia después de newPoller
		out[k] = true
	}
	return out
}

// describe es el texto chico de la tarjeta: la descripción declarada o, si no
// hay, de dónde sale el servicio.
func describe(svc catalog.Service) string {
	if svc.Description != "" {
		return svc.Description
	}
	switch svc.Source {
	case catalog.SourceCompose, catalog.SourceContainer:
		if len(svc.Containers) > 1 {
			return fmt.Sprintf("%s · %d réplicas", shortImage(svc.Containers[0].Image), len(svc.Containers))
		}
		if len(svc.Containers) == 1 {
			return shortImage(svc.Containers[0].Image)
		}
	case catalog.SourceSystemd:
		return svc.Unit
	}
	return ""
}

// shortImage saca el digest de una imagen para mostrarla:
// "valkey/valkey:9@sha256:c123…" → "valkey/valkey:9", y una imagen que es
// solo un digest ("sha256:860aeb…", sin nombre) → "sha256:860aeb5d63ce".
func shortImage(image string) string {
	if name, _, found := strings.Cut(image, "@sha256:"); found {
		return name
	}
	if digest, found := strings.CutPrefix(image, "sha256:"); found && len(digest) > 12 {
		return "sha256:" + digest[:12]
	}
	return image
}

// sourceIcon es el ícono genérico de cada fuente. Los íconos propios de cada
// servicio (labels homepage.icon y maestro.icon) quedan para más adelante.
func sourceIcon(src catalog.Source) string {
	switch src {
	case catalog.SourceSystemd:
		return "settings"
	case catalog.SourceExternal:
		return "public"
	default:
		return "deployed_code"
	}
}
