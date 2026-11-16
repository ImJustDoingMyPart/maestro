// Package checks corre los checks HTTP, TCP y push de los servicios desde
// el hub. Cada check tiene su goroutine; los resultados salen por una
// función que pasa quien crea el Runner (el hub los guarda y recalcula el
// estado).
package checks

import (
	"context"
	"crypto/subtle"
	"hash/fnv"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
)

// Valores por defecto de un check que no los declara.
const (
	DefaultInterval = time.Minute
	DefaultTimeout  = 10 * time.Second
)

// Spec es un check de un servicio.
type Spec struct {
	Service string // ID del servicio en el catálogo
	catalog.Check
}

// Key identifica al check: un servicio tiene como mucho uno de cada tipo.
func (s Spec) Key() Key { return Key{Service: s.Service, Kind: s.Kind} }

func (s Spec) interval() time.Duration {
	if s.Interval > 0 {
		return s.Interval
	}
	return DefaultInterval
}

// timeout nunca pasa del intervalo: si no, un check lento se encimaría con
// el siguiente.
func (s Spec) timeout() time.Duration {
	t := DefaultTimeout
	if s.Timeout > 0 {
		t = s.Timeout
	}
	return min(t, s.interval())
}

// grace es cuánto se espera un push después de su intervalo.
func (s Spec) grace() time.Duration { return s.interval() / 10 }

// PushDeadline es cuánto vale un push: pasado este tiempo sin otro, falla.
func (s Spec) PushDeadline() time.Duration { return s.interval() + s.grace() }

// Key identifica un check.
type Key struct {
	Service string
	Kind    catalog.CheckKind
}

// Result es un resultado de un check.
type Result struct {
	Key
	At      time.Time
	OK      bool
	Latency time.Duration // cero en push
	Detail  string        // "código 200", "timeout (10s)", "no llegó en 96h"…
	// CertExpiry es el vencimiento del certificado del servidor en un check
	// HTTPS que respondió; cero si no aplica.
	CertExpiry time.Time
}

// Options configura un Runner.
type Options struct {
	// Sink recibe cada resultado. Se llama desde la goroutine de cada
	// check, así que tiene que poder llamarse desde varias a la vez.
	Sink func(Result)
	// LastPush da la hora del último push bueno guardado de un check, para
	// que reiniciar el hub no le regale un intervalo entero a un script que
	// ya estaba atrasado. Puede ser nil; cero es "nunca llegó", y entonces
	// se cuenta desde que arranca el check.
	LastPush func(Key) time.Time
	Logger   *slog.Logger
}

// Runner corre un conjunto de checks que se actualiza con Sync.
type Runner struct {
	ctx  context.Context
	opts Options

	mu   sync.Mutex
	jobs map[Key]*job
	wg   sync.WaitGroup
}

type job struct {
	spec   Spec
	cancel context.CancelFunc
	push   chan Ping // solo en push; con lugar para uno
}

// Ping es una llamada a /push/<token>. Down es el status=down de Uptime
// Kuma: el script corrió pero avisa que falló, con Msg como motivo.
type Ping struct {
	At   time.Time
	Down bool
	Msg  string
}

// NewRunner crea un Runner sin checks. Cancelar ctx los para a todos.
func NewRunner(ctx context.Context, opts Options) *Runner {
	return &Runner{ctx: ctx, opts: opts, jobs: make(map[Key]*job)}
}

// Sync deja corriendo exactamente specs: arranca los nuevos, para los que
// ya no están y reinicia los que cambiaron (otra URL, otro intervalo). Los
// que siguen iguales no se tocan.
func (r *Runner) Sync(specs []Spec) {
	want := make(map[Key]Spec, len(specs))
	for _, s := range specs {
		want[s.Key()] = s
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for k, j := range r.jobs {
		if s, ok := want[k]; !ok || s != j.spec {
			j.cancel()
			delete(r.jobs, k)
		}
	}
	for k, s := range want {
		if _, ok := r.jobs[k]; ok || r.ctx.Err() != nil {
			continue
		}
		ctx, cancel := context.WithCancel(r.ctx)
		j := &job{spec: s, cancel: cancel}
		if s.Kind == catalog.CheckPush {
			j.push = make(chan Ping, 1)
		}
		r.jobs[k] = j
		r.wg.Go(func() { r.run(ctx, j) })
	}
}

// Wait espera a que terminen todas las goroutines (después de cancelar el
// contexto de NewRunner).
func (r *Runner) Wait() { r.wg.Wait() }

// Push registra una llamada con token. Dice si el token es de algún check;
// la ruta HTTP no lo cuenta, para no confirmar tokens a quien prueba.
//
// Compara contra todos los tokens en tiempo constante y sin cortar en el
// primero que coincide: el tiempo de respuesta no dice cuánto del token
// acertó ni cuál era.
func (r *Runner) Push(token string, p Ping) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	var match *job
	for _, j := range r.jobs {
		if j.push == nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(j.spec.Target)) == 1 {
			match = j
		}
	}
	if match == nil {
		return false
	}
	// Si ya había una esperando, vale la última: es lo que dijo el script
	// más tarde. Con r.mu tomado nadie más manda, así que hay lugar.
	select {
	case <-match.push:
	default:
	}
	match.push <- p
	return true
}

func (r *Runner) run(ctx context.Context, j *job) {
	if j.push != nil {
		r.runPush(ctx, j)
		return
	}
	var probe func(context.Context) outcome
	switch j.spec.Kind {
	case catalog.CheckHTTP:
		probe = newHTTPProber(j.spec).probe
	case catalog.CheckTCP:
		probe = func(ctx context.Context) outcome { return probeTCP(ctx, j.spec) }
	default:
		r.opts.Logger.Error("tipo de check desconocido", "service", j.spec.Service, "kind", j.spec.Kind)
		return
	}

	// El primer check espera un poco, distinto para cada uno: así los
	// checks de todos los servicios no salen juntos al arrancar el hub.
	timer := time.NewTimer(startDelay(j.spec.Key(), j.spec.interval()))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		r.once(ctx, j, probe)
		timer.Reset(j.spec.interval())
	}
}

// once corre un probe y entrega el resultado. Con recover: un pánico en un
// check queda en el log, esa vuelta no da resultado y la goroutine sigue.
func (r *Runner) once(ctx context.Context, j *job, probe func(context.Context) outcome) {
	res := Result{Key: j.spec.Key(), At: time.Now()}
	defer func() {
		if p := recover(); p != nil {
			r.opts.Logger.Error("pánico en un check", "service", j.spec.Service, "kind", j.spec.Kind,
				"panic", p, "stack", string(debug.Stack()))
		}
	}()
	o := probe(ctx)
	if ctx.Err() != nil {
		return // se paró el check a la mitad: el resultado no vale
	}
	res.OK, res.Latency, res.Detail, res.CertExpiry = o.ok, o.latency, o.detail, o.certExpiry
	r.opts.Sink(res)
}

// runPush espera las llamadas: cada una es un resultado (bueno, o malo si
// avisa una falla), y cada vez que se cumple el plazo sin llamada, uno malo.
func (r *Runner) runPush(ctx context.Context, j *job) {
	key := j.spec.Key()
	start := time.Now()
	if r.opts.LastPush != nil {
		if last := r.opts.LastPush(key); !last.IsZero() {
			start = last
		}
	}
	deadline := start.Add(j.spec.interval() + j.spec.grace())
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-j.push:
			res := Result{Key: key, At: p.At, OK: true, Detail: "llegó"}
			if p.Down {
				res.OK, res.Detail = false, pushDownDetail(p.Msg)
			}
			r.deliver(res)
			// Una llamada con falla también dice que el script corrió: el
			// próximo plazo se cuenta desde ella.
			deadline = p.At.Add(j.spec.interval() + j.spec.grace())
		case <-timer.C:
			r.deliver(Result{Key: key, At: time.Now(), Detail: pushDetail(j.spec.interval())})
			// Sigue fallando una vez por intervalo mientras no llegue.
			deadline = deadline.Add(j.spec.interval())
		}
		timer.Reset(time.Until(deadline))
	}
}

func (r *Runner) deliver(res Result) {
	defer func() {
		if p := recover(); p != nil {
			r.opts.Logger.Error("pánico al entregar un resultado", "service", res.Service, "panic", p,
				"stack", string(debug.Stack()))
		}
	}()
	r.opts.Sink(res)
}

// startDelay reparte el primer check de cada uno en los primeros 10
// segundos (o en el intervalo, si es más corto), siempre igual para el
// mismo check.
func startDelay(k Key, interval time.Duration) time.Duration {
	h := fnv.New32a()
	h.Write([]byte(k.Service + "\x00" + string(k.Kind)))
	window := min(interval, 10*time.Second)
	return time.Duration(h.Sum32()) % window
}
