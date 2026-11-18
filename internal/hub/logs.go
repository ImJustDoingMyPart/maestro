package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

// logOpener abre los logs de un contenedor de un host. Lo cumple *Poller,
// que tiene un cliente por agent; los tests pasan uno falso.
type logOpener interface {
	Logs(ctx context.Context, host, containerID string, opts agent.LogOptions) (iter.Seq2[agent.LogLine, error], error)
}

const (
	// initialTail es cuántas líneas se mandan al abrir, entre todas las
	// réplicas.
	initialTail = 1000
	// maxBatch es cuántas líneas se juntan como mucho antes de un Flush.
	maxBatch = 500
)

// keepalive es cada cuánto se manda un comentario si no hay líneas, para
// que ni Caddy ni el celular den la conexión por muerta. Es variable para
// que los tests no esperen 15 s.
var keepalive = 15 * time.Second

// logEvent es el data de cada evento SSE: una línea, con el contenedor si
// el servicio tiene más de una réplica.
type logEvent struct {
	TS        time.Time `json:"ts"`
	Stream    string    `json:"stream"`
	Line      string    `json:"line"`
	Container string    `json:"c,omitempty"`
	// Level es el nivel cuando la fuente lo dice ("error", "warn"): el
	// journal tiene la prioridad de syslog. Vacío: el visor lo adivina por
	// el texto.
	Level string `json:"lvl,omitempty"`
}

// streamLogs atiende GET /logs/{id...}: los logs de un servicio como
// Server-Sent Events.
//
// Al abrir manda las últimas initialTail líneas de todas las réplicas,
// intercaladas por hora, y después sigue con las nuevas. Cada evento lleva
// la hora de la línea como id: si la conexión se corta, el navegador
// reconecta solo y manda Last-Event-ID, y el stream sigue desde ahí sin
// repetir.
//
// Eventos con nombre: "state" (un cambio de estado del servicio, intercalado
// por hora con las líneas), "notice" (un contenedor no se pudo leer; el resto
// sigue), "stopped" (los contenedores se detuvieron: el stream sigue abierto
// con los cambios de estado y espera a que vuelvan a arrancar), "started"
// (arrancaron de nuevo y siguen los logs) y "end" (se cortó la lectura y no
// hay nada más). Después de "end" el cliente tiene que cerrar el
// EventSource, o el navegador reconectaría.
func (s *server) streamLogs(w http.ResponseWriter, r *http.Request) {
	svc, ok := findService(s.poller.Snapshot(), r.PathValue("id"))
	if !ok {
		http.Error(w, "servicio inexistente", http.StatusNotFound)
		return
	}
	if svc.Source == catalog.SourceSystemd && svc.Unit != "" && len(svc.Containers) == 0 {
		s.streamJournal(w, r, svc)
		return
	}
	cs := svc.Containers
	if name := r.URL.Query().Get("container"); name != "" {
		cs = slices.DeleteFunc(slices.Clone(cs), func(c catalog.Container) bool { return c.Name != name })
	}
	if len(cs) == 0 {
		http.Error(w, "el servicio no tiene contenedores", http.StatusNotFound)
		return
	}
	var resume time.Time
	if id := r.Header.Get("Last-Event-ID"); id != "" {
		resume, _ = time.Parse(time.RFC3339Nano, id) // si no parsea, se arranca de cero
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // por si algún día hay un nginx en el medio
	w.WriteHeader(http.StatusOK)

	// Al salir de acá, por el motivo que sea, se cancela ctx y con él cada
	// pedido abierto a los agents.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sse := &sseWriter{w: w, rc: http.NewResponseController(w)}
	sse.raw("retry: 3000\n\n") // el navegador espera 3 s para reconectar
	if sse.flush() != nil {
		return
	}

	// Suscripto antes de leer el historial, para no perder un cambio que
	// pase en el medio; los repetidos se descartan por ID.
	states, unsubscribe := s.mon.Subscribe(svc.ID)
	defer unsubscribe()
	tl := &timeline{sse: sse, states: states}

	multi := len(svc.Containers) > 1
	since := make(map[string]time.Time, len(cs)) // por contenedor: desde dónde seguir
	if resume.IsZero() {
		lines, notices := s.initialLogs(ctx, cs, multi)
		for _, n := range notices {
			sse.event("notice", n)
		}
		// Los cambios de estado desde la línea más vieja (o del último
		// día, si no hay líneas), intercalados por hora.
		from := time.Now().Add(-historySpan)
		if len(lines) > 0 {
			from = lines[0].TS
		}
		events := s.stateEvents(ctx, svc.ID, from)
		for _, l := range lines {
			for len(events) > 0 && !events[0].At.After(l.TS) {
				tl.send(events[0])
				events = events[1:]
			}
			sse.line(l)
		}
		for _, e := range events {
			tl.send(e)
		}
		for _, l := range lines {
			since[l.containerID] = later(since[l.containerID], l.TS)
		}
	} else {
		for _, c := range cs {
			since[c.ID] = resume
		}
		for _, e := range s.stateEvents(ctx, svc.ID, resume.Add(time.Nanosecond)) {
			tl.send(e)
		}
	}
	if sse.flush() != nil {
		return
	}

	filter := r.URL.Query().Get("container")
	for {
		if s.followLogs(ctx, tl, cs, since, multi) {
			sse.event("end", "Se cortó la lectura de los logs.")
			_ = sse.flush()
			return
		}
		if ctx.Err() != nil {
			return
		}
		sse.event("stopped", "El contenedor se detuvo. Se sigue esperando a que vuelva a arrancar.")
		if sse.flush() != nil {
			return
		}
		started := make(map[string]time.Time, len(cs))
		for _, c := range cs {
			started[c.ID] = c.StartedAt
		}
		var ok bool
		cs, multi, ok = s.waitStart(ctx, tl, svc.ID, filter, started)
		if !ok {
			return
		}
		for _, c := range cs {
			// Un contenedor sin líneas mandadas (nuevo, o uno que no
			// escribió nada) sigue desde su arranque.
			if since[c.ID].IsZero() && !c.StartedAt.IsZero() {
				since[c.ID] = c.StartedAt.Add(-time.Second)
			}
		}
		sse.event("started", "El contenedor arrancó de nuevo.")
		if sse.flush() != nil {
			return
		}
	}
}

// restartPoll es cada cuánto se mira la foto del poller esperando que un
// contenedor detenido vuelva a arrancar. Variable para los tests.
var restartPoll = 2 * time.Second

// timeline manda los cambios de estado sin repetir: el mismo evento puede
// llegar del historial y de la suscripción.
type timeline struct {
	sse       *sseWriter
	states    <-chan store.StateEvent
	lastState int64
}

func (t *timeline) send(e store.StateEvent) {
	if e.ID != 0 && e.ID <= t.lastState {
		return
	}
	t.lastState = max(t.lastState, e.ID)
	t.sse.state(e)
}

// waitStart espera, mandando los cambios de estado y el keepalive, a que
// algún contenedor del servicio (los que pasan filter, si no es vacío)
// arranque: uno que no estaba en started o con una hora de arranque
// posterior. Devuelve los que corren y si el servicio tiene réplicas; ok
// es false si el navegador se fue. Mientras el poller no vea la detención,
// la foto sigue mostrando el arranque viejo, que no cuenta.
func (s *server) waitStart(ctx context.Context, tl *timeline, id, filter string, started map[string]time.Time) (cs []catalog.Container, multi, ok bool) {
	ticker := time.NewTicker(restartPoll)
	defer ticker.Stop()
	lastWrite := time.Now()
	for {
		select {
		case <-ctx.Done():
			return nil, false, false
		case e := <-tl.states:
			tl.send(e)
		case <-ticker.C:
			svc, found := findService(s.poller.Snapshot(), id)
			if found {
				var running []catalog.Container
				fresh := false
				for _, c := range svc.Containers {
					if (filter != "" && c.Name != filter) || c.State != "running" {
						continue
					}
					running = append(running, c)
					if prev, seen := started[c.ID]; !seen || c.StartedAt.After(prev) {
						fresh = true
					}
				}
				if fresh {
					return running, len(svc.Containers) > 1, true
				}
			}
			if time.Since(lastWrite) < keepalive {
				continue
			}
			tl.sse.comment("keepalive")
		}
		if tl.sse.flush() != nil {
			return nil, false, false
		}
		lastWrite = time.Now()
	}
}

// stateEvents lee los cambios de estado de un servicio desde from. Si la
// base falla, la línea de tiempo sigue sin ellos.
func (s *server) stateEvents(ctx context.Context, id string, from time.Time) []store.StateEvent {
	_, events, err := s.mon.History(ctx, id, from)
	if err != nil {
		s.logger.Warn("leer cambios de estado", "service", id, "err", err)
	}
	return events
}

// taggedEvent es un logEvent con el ID del contenedor del que salió.
type taggedEvent struct {
	logEvent
	containerID string
}

// initialLogs pide las últimas líneas de cada contenedor a la vez, las
// intercala por hora y se queda con las últimas initialTail. Un contenedor
// que falla se avisa y no frena a los demás.
func (s *server) initialLogs(ctx context.Context, cs []catalog.Container, multi bool) ([]taggedEvent, []string) {
	var (
		mu      sync.Mutex
		all     []taggedEvent
		notices []string
		wg      sync.WaitGroup
	)
	for _, c := range cs {
		wg.Go(func() {
			defer s.recoverAs(func(err error) {
				mu.Lock()
				notices = append(notices, fmt.Sprintf("%s: %v", c.Name, err))
				mu.Unlock()
			})
			var mine []taggedEvent
			err := s.readLogs(ctx, c, agent.LogOptions{Tail: initialTail}, func(l agent.LogLine) bool {
				mine = append(mine, tag(c, l, multi))
				return true
			})
			mu.Lock()
			defer mu.Unlock()
			all = append(all, mine...)
			if err != nil {
				notices = append(notices, fmt.Sprintf("%s: %v", c.Name, err))
			}
		})
	}
	wg.Wait()

	// Stable: dentro de un contenedor, dos líneas con la misma hora
	// conservan su orden.
	slices.SortStableFunc(all, func(a, b taggedEvent) int { return a.TS.Compare(b.TS) })
	if len(all) > initialTail {
		all = all[len(all)-initialTail:]
	}
	slices.Sort(notices)
	return all, notices
}

// followLogs sigue a todos los contenedores a la vez hasta que se corte el
// pedido o terminen todos, y manda lo que llega en tandas.
// Manda también los cambios de estado que llegan por tl. Anota en since la
// hora de la última línea mandada de cada contenedor, para seguir desde ahí
// si vuelve a arrancar. Devuelve true si la lectura se cortó por un error
// (no porque los contenedores se detuvieron, ni porque el navegador se
// fue).
func (s *server) followLogs(ctx context.Context, tl *timeline, cs []catalog.Container, since map[string]time.Time, multi bool) bool {
	sse := tl.sse
	type msg struct {
		ev          *logEvent // nil si es un aviso
		containerID string
		notice      string
	}
	ch := make(chan msg)
	var (
		wg     sync.WaitGroup
		failed atomic.Bool // alguno terminó con error, no porque se detuvo
	)
	send := func(m msg) bool {
		select {
		case ch <- m:
			return true
		case <-ctx.Done():
			return false
		}
	}

	for _, c := range cs {
		// Se lee acá y no en la goroutine: el bucle de abajo escribe en
		// since mientras las goroutines corren.
		from := since[c.ID]
		wg.Go(func() {
			defer s.recoverAs(func(err error) {
				failed.Store(true)
				send(msg{notice: fmt.Sprintf("%s: %v", c.Name, err)})
			})
			opts := agent.LogOptions{Follow: true, Tail: 0}
			if !from.IsZero() {
				// Since de Docker incluye la línea de esa hora exacta, que
				// ya se mandó: se filtra abajo. Tail alto para no perder
				// nada si hubo mucho movimiento durante un corte.
				opts.Since, opts.Tail = from, agent.MaxLogTail
			}
			err := s.readLogs(ctx, c, opts, func(l agent.LogLine) bool {
				if !from.IsZero() && !l.TS.After(from) {
					return true
				}
				ev := tag(c, l, multi).logEvent
				return send(msg{ev: &ev, containerID: c.ID})
			})
			if err != nil && ctx.Err() == nil {
				failed.Store(true)
				send(msg{notice: fmt.Sprintf("%s: %v", c.Name, err)})
			}
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	// El ticker hace dos cosas: avisar las líneas omitidas cuando el
	// contenedor deja de inundar, y el keepalive si no se mandó nada.
	ticker := time.NewTicker(min(250*time.Millisecond, keepalive))
	defer ticker.Stop()
	lastWrite := time.Now()
	lim := limiter{max: maxRate}
	skippedNotice := func(n int) {
		sse.event("notice", fmt.Sprintf("Se omitieron %d líneas: llegan más de %d por segundo.", n, maxRate))
	}
	write := func(m msg) {
		if m.ev == nil {
			sse.event("notice", m.notice)
			return
		}
		ok, skipped := lim.allow(time.Now())
		if skipped > 0 {
			skippedNotice(skipped)
		}
		if ok {
			sse.line(taggedEvent{logEvent: *m.ev})
			since[m.containerID] = later(since[m.containerID], m.ev.TS)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case <-done:
			// Lo que quedó en el canal ya se mandó: los que escriben en ch
			// terminaron antes de cerrar done.
			return failed.Load()
		case e := <-tl.states:
			tl.send(e)
		case now := <-ticker.C:
			if n := lim.expired(now); n > 0 {
				skippedNotice(n)
			} else if now.Sub(lastWrite) >= keepalive {
				sse.comment("keepalive")
			} else {
				continue // no se escribió nada: no hace falta Flush
			}
		case m := <-ch:
			// Se junta lo que ya esté esperando antes de mandar: con un
			// contenedor que escribe miles de líneas por segundo, un Flush
			// por línea sería un paquete por línea.
			write(m)
		drain: // un break suelto saldría solo del select, no del for
			for n := 1; n < maxBatch; n++ {
				select {
				case m := <-ch:
					write(m)
				default:
					break drain
				}
			}
		}
		if sse.flush() != nil {
			return false // el navegador se fue; el defer cancel() corta a los agents
		}
		lastWrite = time.Now()
	}
}

// maxRate es cuántas líneas por segundo se mandan como mucho por stream.
// Un contenedor que escribe decenas de miles por segundo ahogaría al
// navegador del celular solo con parsear los eventos, y nadie las lee a
// ese ritmo: las de más se omiten y se avisa cuántas. Es variable para los
// tests.
var maxRate = 2000

// limiter cuenta líneas por ventanas de un segundo.
type limiter struct {
	max     int
	window  time.Time // comienzo de la ventana actual
	n       int       // líneas que pasaron en la ventana
	skipped int       // omitidas desde el último aviso
}

// allow dice si una línea que llega en now pasa. Si empezó una ventana
// nueva y en la anterior se omitieron líneas, devuelve cuántas, para
// avisarlo antes de la línea.
func (l *limiter) allow(now time.Time) (ok bool, skipped int) {
	if now.Sub(l.window) >= time.Second {
		l.window, l.n = now, 0
		skipped, l.skipped = l.skipped, 0
	}
	if l.n >= l.max {
		l.skipped++
		return false, skipped
	}
	l.n++
	return true, skipped
}

// expired devuelve las omitidas si la ventana ya terminó y no llegó nada
// después (el contenedor dejó de inundar).
func (l *limiter) expired(now time.Time) int {
	if l.skipped == 0 || now.Sub(l.window) < time.Second {
		return 0
	}
	n := l.skipped
	l.skipped = 0
	return n
}

// readLogs abre los logs de un contenedor y llama a fn por línea.
func (s *server) readLogs(ctx context.Context, c catalog.Container, opts agent.LogOptions, fn func(agent.LogLine) bool) error {
	lines, err := s.logs.Logs(ctx, c.Host, c.ID, opts)
	if errors.Is(err, agent.ErrContainerNotFound) {
		return errors.New("el contenedor ya no existe")
	}
	if err != nil {
		s.logger.Warn("abrir logs", "host", c.Host, "container", c.Name, "err", err)
		return errors.New("no se pudieron leer los logs (ver el log del hub)")
	}
	for l, err := range lines {
		if err != nil {
			if ctx.Err() == nil {
				s.logger.Warn("leer logs", "host", c.Host, "container", c.Name, "err", err)
				return errors.New("se cortó la lectura de los logs")
			}
			return nil
		}
		if !fn(l) {
			return nil
		}
	}
	return nil
}

// recoverAs atrapa un pánico de la goroutine que lo difiere y lo pasa a
// report como error: un contenedor que hace fallar algo no tira el hub.
func (s *server) recoverAs(report func(error)) {
	if r := recover(); r != nil {
		s.logger.Error("pánico leyendo logs", "panic", r, "stack", string(debug.Stack()))
		report(errors.New("error interno"))
	}
}

func tag(c catalog.Container, l agent.LogLine, multi bool) taggedEvent {
	ev := taggedEvent{logEvent: logEvent{TS: l.TS, Stream: l.Stream, Line: l.Line}, containerID: c.ID}
	if multi {
		ev.Container = c.Name
	}
	return ev
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// findService busca un servicio por ID en la foto del poller.
func findService(snap Snapshot, id string) (catalog.Service, bool) {
	i := slices.IndexFunc(snap.Services, func(s catalog.Service) bool { return s.ID == id })
	if i >= 0 {
		return snap.Services[i], true
	}
	// Lo descubierto tiene página aunque no se muestre en la portada.
	if j := slices.IndexFunc(snap.Discovered, func(d Discovered) bool { return d.Key() == id }); j >= 0 {
		return foundService(snap.Discovered[j]), true
	}
	return catalog.Service{}, false
}

// sseWriter escribe en el formato de Server-Sent Events: campos "nombre:
// valor", un evento por bloque terminado en línea vacía. Los errores de
// escritura se ven en flush: si el cliente se fue, falla.
type sseWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (s *sseWriter) raw(text string) { _, _ = s.w.Write([]byte(text)) }

// comment manda una línea que el navegador ignora: sirve de keepalive.
func (s *sseWriter) comment(text string) { s.raw(": " + text + "\n\n") }

// line manda una línea de log como evento sin nombre ("message"), con la
// hora como id para reconectar.
func (s *sseWriter) line(ev taggedEvent) {
	data, _ := json.Marshal(ev.logEvent) // no falla: son strings y un time.Time
	if !ev.TS.IsZero() {
		s.raw("id: " + ev.TS.Format(time.RFC3339Nano) + "\n")
	}
	s.raw("data: " + string(data) + "\n\n")
}

// stateEvent es el data de un evento "state".
type stateEvent struct {
	TS     time.Time `json:"ts"`
	State  string    `json:"state"` // "up", "degraded", "down"
	Label  string    `json:"label"` // "Caído"
	Reason string    `json:"reason,omitempty"`
}

// state manda un cambio de estado y devuelve su ID. No lleva id de SSE: el
// id es la hora de la última línea de log, desde donde se reconecta.
func (s *sseWriter) state(e store.StateEvent) int64 {
	data, _ := json.Marshal(stateEvent{TS: e.At, State: e.To, Label: State(e.To).Label(), Reason: shownReason(e.Reason)})
	s.raw("event: state\ndata: " + string(data) + "\n\n")
	return e.ID
}

// event manda un evento con nombre y un texto. El texto va como JSON para
// que un salto de línea no corte el campo data.
func (s *sseWriter) event(name, text string) {
	data, _ := json.Marshal(text)
	s.raw("event: " + name + "\ndata: " + string(data) + "\n\n")
}

func (s *sseWriter) flush() error { return s.rc.Flush() }
