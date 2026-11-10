package hub

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/checks"
	"github.com/ImJustDoingMyPart/maestro/internal/status"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

const (
	// keepRaw es cuánto se guardan los resultados crudos de los checks;
	// después quedan los resúmenes por hora.
	keepRaw = 7 * 24 * time.Hour
	// keepHourly es cuánto se guardan los resúmenes por hora.
	keepHourly = 365 * 24 * time.Hour
	// compactEvery es cada cuánto se compacta.
	compactEvery = time.Hour
	// storeTimeout acota cada escritura en la base.
	storeTimeout = 5 * time.Second
)

// checkState es lo acumulado de un check: lo que status necesita, más el
// último resultado para mostrar.
type checkState struct {
	results    int
	failures   int
	last       checks.Result
	certExpiry time.Time // del último resultado bueno
}

// containerSeen es lo que se recuerda de un contenedor entre vueltas para
// ver si reinició.
type containerSeen struct {
	startedAt   time.Time
	restartedAt time.Time // cero si el hub no lo vio reiniciar
}

// Monitor junta los checks, el estado de los contenedores y los eventos.
// Recibe cada foto del poller (Update) y cada resultado de un check, guarda
// los resultados y los cambios de estado en la base y responde el estado
// actual de cada servicio.
type Monitor struct {
	store  *store.Store
	runner *checks.Runner
	params status.Params
	logger *slog.Logger
	now    func() time.Time

	mu         sync.Mutex
	services   map[string]catalog.Service
	hostOK     map[string]bool
	checks     map[checks.Key]*checkState
	containers map[string]*containerSeen // por ID de contenedor
	current    map[string]status.Status  // último estado calculado
	recorded   map[string]status.Status  // último estado guardado como evento
	subs       map[string]map[chan store.StateEvent]struct{}
}

// NewMonitor arma el monitor y lee de la base lo que hace falta para seguir
// donde quedó: las últimas fallas de cada check y el último estado guardado
// de cada servicio. Los checks corren hasta que se cancele ctx.
func NewMonitor(ctx context.Context, st *store.Store, logger *slog.Logger) (*Monitor, error) {
	m := &Monitor{
		store:      st,
		params:     status.DefaultParams,
		logger:     logger,
		now:        time.Now,
		services:   map[string]catalog.Service{},
		hostOK:     map[string]bool{},
		checks:     map[checks.Key]*checkState{},
		containers: map[string]*containerSeen{},
		current:    map[string]status.Status{},
		recorded:   map[string]status.Status{},
		subs:       map[string]map[chan store.StateEvent]struct{}{},
	}

	// Con los últimos FailThreshold resultados alcanza para saber si un
	// check está caído; uno más para que el conteo no se corte justo ahí.
	latest, err := st.LatestCheckResults(ctx, m.params.FailThreshold+1)
	if err != nil {
		return nil, err
	}
	// Vienen del más nuevo al más viejo dentro de cada check.
	for _, r := range latest {
		k := checks.Key{Service: r.Service, Kind: catalog.CheckKind(r.Kind)}
		cs := m.checks[k]
		if cs == nil {
			cs = &checkState{last: fromStored(r), failures: -1}
			m.checks[k] = cs
		}
		cs.results++
		if r.OK {
			if cs.failures < 0 {
				cs.failures = cs.results - 1
			}
			if cs.certExpiry.IsZero() {
				cs.certExpiry = r.CertExpiry
			}
		}
	}
	for _, cs := range m.checks {
		if cs.failures < 0 { // ninguno bueno entre los leídos
			cs.failures = cs.results
		}
	}

	events, err := st.LastStateEvents(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		m.recorded[e.Service] = recordedStatus(e)
	}

	m.runner = checks.NewRunner(ctx, checks.Options{
		Sink:   m.onResult,
		Logger: logger,
		LastPush: func(k checks.Key) time.Time {
			ctx, cancel := context.WithTimeout(ctx, storeTimeout)
			defer cancel()
			at, err := st.LastOK(ctx, k.Service, string(k.Kind))
			if err != nil {
				logger.Warn("leer el último push", "service", k.Service, "err", err)
			}
			return at
		},
	})
	return m, nil
}

// recordedStatus pasa un evento guardado al Status con el que se compara el
// próximo. Un evento a Up guarda "arriba" o "volvió a responder" como
// motivo, pero un Status Up no tiene motivos: si se lo dejara, el próximo
// Up (sin motivo) parecería distinto.
func recordedStatus(e store.StateEvent) status.Status {
	s := status.Status{State: status.State(e.To)}
	if s.State != status.Up {
		s.Reasons = []string{e.Reason}
	}
	return s
}

func fromStored(r store.CheckResult) checks.Result {
	return checks.Result{
		Key: checks.Key{Service: r.Service, Kind: catalog.CheckKind(r.Kind)},
		At:  r.At, OK: r.OK, Latency: r.Latency, Detail: r.Detail, CertExpiry: r.CertExpiry,
	}
}

// Run compacta la base cada hora hasta que se cancele ctx, y al salir
// espera a que terminen los checks.
func (m *Monitor) Run(ctx context.Context) {
	defer m.runner.Wait()
	m.compact(ctx)
	ticker := time.NewTicker(compactEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.compact(ctx)
		}
	}
}

func (m *Monitor) compact(ctx context.Context) {
	defer m.recoverLog("compactar")
	now := m.now()
	n, err := m.store.Compact(ctx, now.Add(-keepRaw), now.Add(-keepHourly))
	if err != nil {
		m.logger.Error("compactar resultados", "err", err)
		return
	}
	if n > 0 {
		m.logger.Info("resultados compactados", "rows", n)
	}
}

// Push registra la llamada de un script. Dice si el token es de algún
// check.
func (m *Monitor) Push(token string) bool {
	return m.runner.Push(token, m.now())
}

// Update recibe una foto nueva del poller: actualiza los servicios, los
// checks que corren y el estado de todos.
func (m *Monitor) Update(snap Snapshot) {
	defer m.recoverLog("actualizar estado")
	var specs []checks.Spec

	m.mu.Lock()
	m.services = make(map[string]catalog.Service, len(snap.Services))
	seen := make(map[string]bool)
	for _, svc := range snap.Services {
		m.services[svc.ID] = svc
		for _, c := range svc.Checks {
			specs = append(specs, checks.Spec{Service: svc.ID, Check: c})
		}
		for _, c := range svc.Containers {
			seen[c.ID] = true
			m.trackContainer(c, snap.HostOK(c.Host))
		}
	}
	for id := range m.containers {
		if !seen[id] {
			delete(m.containers, id)
		}
	}
	for _, h := range snap.Hosts {
		m.hostOK[h.Name] = h.OK
	}
	for id := range m.current {
		if _, ok := m.services[id]; !ok {
			delete(m.current, id)
		}
	}
	for id := range m.services {
		m.evaluate(id)
	}
	m.mu.Unlock()

	m.runner.Sync(specs) // fuera del lock: Sync no llama de vuelta, pero no hace falta tenerlo
}

// trackContainer anota la hora de arranque y, si cambió desde la vuelta
// anterior, que reinició. Con el host sin responder los datos son viejos y
// no se tocan.
func (m *Monitor) trackContainer(c catalog.Container, hostOK bool) {
	if !hostOK {
		return
	}
	cs := m.containers[c.ID]
	if cs == nil {
		m.containers[c.ID] = &containerSeen{startedAt: c.StartedAt}
		return
	}
	if !c.StartedAt.IsZero() && c.StartedAt.After(cs.startedAt) && !cs.startedAt.IsZero() {
		cs.restartedAt = c.StartedAt
	}
	cs.startedAt = c.StartedAt
}

// onResult guarda un resultado y recalcula el estado de su servicio. Lo
// llaman las goroutines de los checks.
func (m *Monitor) onResult(r checks.Result) {
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	if err := m.store.AddCheckResult(ctx, store.CheckResult{
		Service: r.Service, Kind: string(r.Kind), At: r.At, OK: r.OK,
		Latency: r.Latency, Detail: r.Detail, CertExpiry: r.CertExpiry,
	}); err != nil {
		m.logger.Error("guardar resultado de un check", "service", r.Service, "kind", r.Kind, "err", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	cs := m.checks[r.Key]
	if cs == nil {
		cs = &checkState{}
		m.checks[r.Key] = cs
	}
	cs.results++
	cs.last = r
	if r.OK {
		cs.failures = 0
		cs.certExpiry = r.CertExpiry
	} else {
		cs.failures++
	}
	if _, ok := m.services[r.Service]; ok {
		m.evaluate(r.Service)
	}
}

// evaluate recalcula el estado de un servicio y, si cambió, guarda el
// evento y avisa a quien esté mirando. Se llama con m.mu tomado.
func (m *Monitor) evaluate(id string) {
	svc := m.services[id]
	in := buildInput(svc, m.hostOK[svc.Host],
		func(containerID string) time.Time {
			if seen := m.containers[containerID]; seen != nil {
				return seen.restartedAt
			}
			return time.Time{}
		},
		func(k checks.Key) *checkState { return m.checks[k] })

	now := m.now()
	next := status.Evaluate(in, now, m.params)
	m.current[id] = next
	ev, ok := status.Change(m.recorded[id], next)
	if !ok {
		return
	}
	m.recorded[id] = next

	e := store.StateEvent{Service: id, At: now, From: string(ev.From), To: string(ev.To), Reason: ev.Reason}
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	evID, err := m.store.AddStateEvent(ctx, e)
	if err != nil {
		m.logger.Error("guardar cambio de estado", "service", id, "err", err)
	}
	e.ID = evID
	m.logger.Info("cambio de estado", "service", id, "from", ev.From, "to", ev.To, "reason", ev.Reason)
	for ch := range m.subs[id] {
		select {
		case ch <- e:
		default: // quien mira está atrasado: se pierde este, no se traba el monitor
		}
	}
}

// buildInput arma lo que status.Evaluate necesita de un servicio.
// restarted y check pueden ser nil (sin reinicios vistos, sin resultados).
func buildInput(svc catalog.Service, hostOK bool, restarted func(containerID string) time.Time, check func(checks.Key) *checkState) status.Input {
	in := status.Input{
		FromContainers: svc.Source == catalog.SourceCompose || svc.Source == catalog.SourceContainer,
		HostOK:         hostOK,
	}
	for _, c := range svc.Containers {
		sc := status.Container{Name: c.Name, State: c.State, Health: c.Health, ExitCode: c.ExitCode}
		if restarted != nil {
			sc.RestartedAt = restarted(c.ID)
		}
		in.Containers = append(in.Containers, sc)
	}
	for _, c := range svc.Checks {
		sc := status.Check{Kind: string(c.Kind)}
		if check != nil {
			if cs := check(checks.Key{Service: svc.ID, Kind: c.Kind}); cs != nil {
				sc.Results, sc.Failures, sc.Detail, sc.CertExpiry = cs.results, cs.failures, cs.last.Detail, cs.certExpiry
			}
		}
		in.Checks = append(in.Checks, sc)
	}
	return in
}

// Status es el estado actual de un servicio. Unknown con "sin datos
// todavía" si el monitor todavía no lo evaluó.
func (m *Monitor) Status(id string) status.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.current[id]; ok {
		return s
	}
	return status.Status{State: status.Unknown, Reasons: []string{"sin datos todavía"}}
}

// CheckView es el último resultado de un check, para mostrar.
type CheckView struct {
	Kind     catalog.CheckKind
	Target   string // vacío en push: es el token
	Last     checks.Result
	Results  int // 0: todavía no corrió
	Failures int
}

// Checks da el estado de los checks declarados de un servicio, en el orden
// del catálogo.
func (m *Monitor) Checks(id string) []CheckView {
	m.mu.Lock()
	defer m.mu.Unlock()
	svc, ok := m.services[id]
	if !ok {
		return nil
	}
	out := make([]CheckView, 0, len(svc.Checks))
	for _, c := range svc.Checks {
		v := CheckView{Kind: c.Kind, Target: c.Target}
		if c.Kind == catalog.CheckPush {
			v.Target = ""
		}
		if cs := m.checks[checks.Key{Service: id, Kind: c.Kind}]; cs != nil {
			v.Last, v.Results, v.Failures = cs.last, cs.results, cs.failures
		}
		out = append(out, v)
	}
	return out
}

// Subscribe devuelve un canal con los cambios de estado de un servicio a
// partir de ahora. Hay que llamar a cancel al dejar de leer.
func (m *Monitor) Subscribe(id string) (<-chan store.StateEvent, func()) {
	ch := make(chan store.StateEvent, 16)
	m.mu.Lock()
	if m.subs[id] == nil {
		m.subs[id] = map[chan store.StateEvent]struct{}{}
	}
	m.subs[id][ch] = struct{}{}
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		delete(m.subs[id], ch)
		if len(m.subs[id]) == 0 {
			delete(m.subs, id)
		}
		m.mu.Unlock()
	}
}

// History da los resultados de los checks y los cambios de estado de un
// servicio desde since.
func (m *Monitor) History(ctx context.Context, id string, since time.Time) ([]store.CheckResult, []store.StateEvent, error) {
	results, err := m.store.CheckResults(ctx, id, since)
	if err != nil {
		return nil, nil, err
	}
	events, err := m.store.StateEvents(ctx, id, since, maxEvents)
	return results, events, err
}

// maxEvents es cuántos cambios de estado se muestran como mucho.
const maxEvents = 500

func (m *Monitor) recoverLog(what string) {
	if r := recover(); r != nil {
		m.logger.Error("pánico al "+what, "panic", r, "stack", string(debug.Stack()))
	}
}
