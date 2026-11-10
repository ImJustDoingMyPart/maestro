package hub

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/config"
)

// containerLister es lo que el poller necesita de un agent. Lo cumple
// *agent.Client; los tests pasan uno falso.
type containerLister interface {
	Containers(ctx context.Context) ([]agent.Container, error)
}

// HostStatus es cómo le fue al último pedido a un agent.
type HostStatus struct {
	Name   string
	OK     bool
	Err    string    // vacío si OK
	LastOK time.Time // cero si nunca respondió
}

// Snapshot es la foto que muestra la portada.
type Snapshot struct {
	Services []catalog.Service
	Hosts    []HostStatus // ordenados por nombre
	At       time.Time    // cero hasta la primera vuelta
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
}

// Poller consulta a los agents cada intervalo y arma la lista de servicios.
type Poller struct {
	agents   map[string]containerLister
	file     map[string]catalog.FileService
	catalog  *catalog.Catalog
	interval time.Duration
	timeout  time.Duration
	logger   *slog.Logger
	now      func() time.Time

	mu    sync.RWMutex
	hosts map[string]*hostState
	snap  Snapshot
}

// NewPoller arma un poller con un cliente por host de la config.
func NewPoller(cfg *config.Config, logger *slog.Logger) *Poller {
	agents := make(map[string]containerLister, len(cfg.Hosts))
	for name, h := range cfg.Hosts {
		agents[name] = agent.NewClient(h.Agent, h.Token)
	}
	return newPoller(agents, cfg.FileServices(), logger)
}

func newPoller(agents map[string]containerLister, file map[string]catalog.FileService, logger *slog.Logger) *Poller {
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
}

// poll pide los contenedores a todos los agents a la vez y rearma la foto.
func (p *Poller) poll(ctx context.Context) {
	type result struct {
		host string
		cs   []agent.Container
		err  error
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
	}

	var all []catalog.Container
	hosts := make([]HostStatus, 0, len(p.hosts))
	for _, name := range slices.Sorted(maps.Keys(p.hosts)) {
		h := p.hosts[name]
		hosts = append(hosts, h.status)
		for _, c := range h.containers {
			all = append(all, catalog.Container{
				Host: name, ID: c.ID, Name: c.Name, Image: c.Image,
				State: c.State, Health: c.Health, Restarts: c.Restarts, Labels: c.Labels,
			})
		}
	}
	p.snap = Snapshot{Services: p.catalog.Build(all, p.file), Hosts: hosts, At: now}
}

// serviceState resume un servicio en el semáforo. En la etapa 1 solo se mira
// el estado de los contenedores: los checks y las unidades systemd llegan
// en las etapas 4 y 6, así que esos servicios quedan sin datos.
func serviceState(svc catalog.Service, snap Snapshot) State {
	switch svc.Source {
	case catalog.SourceCompose, catalog.SourceContainer:
	default:
		return StateUnknown
	}
	if !snap.HostOK(svc.Host) {
		return StateUnknown // datos viejos: no se afirma nada
	}
	state := StateUp
	for _, c := range svc.Containers {
		switch {
		case c.State != "running" || c.Health == "unhealthy":
			return StateDown
		case c.Health == "starting":
			state = StateUnknown
		}
	}
	return state
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
