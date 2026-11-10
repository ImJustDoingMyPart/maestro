package hub

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// maxRunLines acota la salida que se guarda de una acción. Un pull de un
// stack grande da unos cientos de renglones; esto es un tope de seguridad.
const maxRunLines = 20000

// runKeep es cuánto se guarda una acción terminada. Mientras tanto, la
// página del stack la muestra al abrirla y el navegador puede reconectarse.
const runKeep = time.Hour

// runRecent es cuánto después de terminar una acción la página del stack la
// sigue mostrando al abrirla.
const runRecent = 10 * time.Minute

type runLine struct {
	TS     time.Time `json:"ts"`
	Stream string    `json:"stream"`
	Line   string    `json:"line"`
}

// run es una acción de compose en curso o terminada. El hub lee la salida
// del agent hasta el final, haya o no un navegador mirando, y la guarda acá.
type run struct {
	ID      string
	Host    string
	Project string
	Action  string
	Started time.Time

	mu       sync.Mutex
	lines    []runLine
	dropped  int
	done     bool
	exit     int
	errMsg   string
	finished time.Time
	// wake se cierra en cada cambio y se reemplaza por uno nuevo: quien
	// espera novedades espera a que se cierre. Es la forma de avisar a
	// muchos a la vez con canales (un sync.Cond no se puede combinar con un
	// select sobre el contexto del pedido).
	wake chan struct{}
}

func newRun(host, project, action string, now time.Time) *run {
	return &run{
		ID: rand.Text(), Host: host, Project: project, Action: action, Started: now,
		wake: make(chan struct{}),
	}
}

func (r *run) notify() {
	close(r.wake)
	r.wake = make(chan struct{})
}

func (r *run) append(l runLine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.lines) >= maxRunLines {
		r.dropped++
		return
	}
	r.lines = append(r.lines, l)
	r.notify()
}

func (r *run) finish(exit int, errMsg string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.done, r.exit, r.errMsg, r.finished = true, exit, errMsg, now
	r.notify()
}

// since devuelve las líneas desde la i (contando desde 0), si la acción
// terminó, y un canal que se cierra con el próximo cambio.
func (r *run) since(i int) (lines []runLine, done bool, wake <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < len(r.lines) {
		lines = r.lines[i:len(r.lines):len(r.lines)] // sin capacidad de más: nadie puede escribir en el arreglo compartido
	}
	return lines, r.done, r.wake
}

// running dice si la acción sigue, o si terminó hace menos de d.
func (r *run) recent(now time.Time, d time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.done || now.Sub(r.finished) < d
}

// summary es el texto del final.
func (r *run) summary() (text string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.errMsg != "":
		text = fmt.Sprintf("%s falló: %s", r.Action, r.errMsg)
	case r.exit == 0:
		text = fmt.Sprintf("%s terminó bien.", r.Action)
	default:
		text = fmt.Sprintf("%s terminó con error (código %d).", r.Action, r.exit)
	}
	if r.dropped > 0 {
		text += fmt.Sprintf(" Se omitieron %d renglones de salida.", r.dropped)
	}
	return text, r.errMsg == "" && r.exit == 0
}

// runStore guarda las acciones de la última hora.
type runStore struct {
	mu   sync.Mutex
	byID map[string]*run
	last map[string]*run // "<host>/<proyecto>" → la última acción
	now  func() time.Time
}

func newRunStore() *runStore {
	return &runStore{byID: make(map[string]*run), last: make(map[string]*run), now: time.Now}
}

func (s *runStore) add(r *run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, old := range s.byID {
		old.mu.Lock()
		expired := old.done && now.Sub(old.finished) > runKeep
		old.mu.Unlock()
		if expired {
			delete(s.byID, id)
			if s.last[old.Host+"/"+old.Project] == old {
				delete(s.last, old.Host+"/"+old.Project)
			}
		}
	}
	s.byID[r.ID] = r
	s.last[r.Host+"/"+r.Project] = r
}

func (s *runStore) get(id string) (*run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.byID[id]
	return r, ok
}

// latest devuelve la última acción del stack si sigue corriendo o terminó
// hace poco.
func (s *runStore) latest(host, project string) (*run, bool) {
	s.mu.Lock()
	r, ok := s.last[host+"/"+project]
	s.mu.Unlock()
	if !ok || !r.recent(s.now(), runRecent) {
		return nil, false
	}
	return r, true
}
