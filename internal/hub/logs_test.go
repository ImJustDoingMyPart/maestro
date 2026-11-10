package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/auth"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

// fakeLogs imita a Docker por contenedor: un historial y, con Follow, un
// canal de líneas nuevas; cerrar el canal es que el contenedor se detuvo.
type fakeLogs struct {
	history map[string][]agent.LogLine
	live    map[string]chan agent.LogLine
	openErr map[string]error

	mu       sync.Mutex
	calls    []logCall
	abiertas atomic.Int32
}

type logCall struct {
	host, id string
	opts     agent.LogOptions
}

func (f *fakeLogs) Logs(ctx context.Context, host, id string, opts agent.LogOptions) (iter.Seq2[agent.LogLine, error], error) {
	f.mu.Lock()
	f.calls = append(f.calls, logCall{host, id, opts})
	f.mu.Unlock()
	if err := f.openErr[id]; err != nil {
		return nil, err
	}

	// Como Docker: since incluye la hora exacta, y tail se aplica después.
	var hist []agent.LogLine
	for _, l := range f.history[id] {
		if opts.Since.IsZero() || !l.TS.Before(opts.Since) {
			hist = append(hist, l)
		}
	}
	if len(hist) > opts.Tail {
		hist = hist[len(hist)-opts.Tail:]
	}

	f.mu.Lock()
	live := f.live[id] // se toma al abrir: un test puede cambiarlo para un arranque nuevo
	f.mu.Unlock()
	f.abiertas.Add(1)
	return func(yield func(agent.LogLine, error) bool) {
		defer f.abiertas.Add(-1)
		for _, l := range hist {
			if !yield(l, nil) {
				return
			}
		}
		if !opts.Follow {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case l, ok := <-live:
				if !ok || !yield(l, nil) {
					return
				}
			}
		}
	}, nil
}

func (f *fakeLogs) callsFor(id string) []logCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []logCall
	for _, c := range f.calls {
		if c.id == id {
			out = append(out, c)
		}
	}
	return out
}

var t0 = time.Date(2026, 11, 10, 14, 0, 0, 0, time.UTC)

func line(sec int, text string) agent.LogLine {
	return agent.LogLine{TS: t0.Add(time.Duration(sec) * time.Second), Stream: "stdout", Line: text}
}

// sseEvent es un evento ya leído. Un comentario (": ...") viene solo con
// Comment.
type sseEvent struct {
	ID, Event, Data, Comment string
}

// sseReader lee eventos de un stream en otra goroutine, para poder esperar
// con timeout.
type sseReader struct {
	ch chan sseEvent
}

func newSSEReader(r io.Reader) *sseReader {
	s := &sseReader{ch: make(chan sseEvent, 4096)}
	go func() {
		defer close(s.ch)
		sc := bufio.NewScanner(r)
		sc.Buffer(nil, 1<<20)
		var ev sseEvent
		var any bool
		for sc.Scan() {
			l := sc.Text()
			switch {
			case l == "":
				if any {
					s.ch <- ev
				}
				ev, any = sseEvent{}, false
			case strings.HasPrefix(l, ": "):
				s.ch <- sseEvent{Comment: l[2:]}
			default:
				k, v, _ := strings.Cut(l, ": ")
				switch k {
				case "id":
					ev.ID = v
				case "event":
					ev.Event = v
				case "data":
					ev.Data = v
				}
				any = true
			}
		}
	}()
	return s
}

// next devuelve el próximo evento que no sea comentario ni retry.
func (s *sseReader) next(t *testing.T) sseEvent {
	t.Helper()
	for {
		select {
		case ev, ok := <-s.ch:
			if !ok {
				t.Fatal("el stream terminó")
			}
			if ev.Comment != "" || (ev.Event == "" && ev.Data == "") {
				continue
			}
			return ev
		case <-time.After(2 * time.Second):
			t.Fatal("pasaron 2 s sin eventos")
		}
	}
}

func (s *sseReader) nextLine(t *testing.T) logEvent {
	t.Helper()
	ev := s.next(t)
	if ev.Event != "" {
		t.Fatalf("quería una línea, llegó el evento %q: %s", ev.Event, ev.Data)
	}
	var l logEvent
	if err := json.Unmarshal([]byte(ev.Data), &l); err != nil {
		t.Fatalf("data %q: %v", ev.Data, err)
	}
	if !l.TS.IsZero() && ev.ID != l.TS.Format(time.RFC3339Nano) {
		t.Errorf("id = %q, quería la hora de la línea", ev.ID)
	}
	return l
}

func (s *sseReader) closed(t *testing.T) {
	t.Helper()
	for {
		select {
		case ev, ok := <-s.ch:
			if !ok {
				return
			}
			if ev.Comment == "" && (ev.Event != "" || ev.Data != "") {
				t.Fatalf("quería el fin del stream, llegó %+v", ev)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("el stream no terminó")
		}
	}
}

func svcWith(cs ...string) catalog.Service {
	s := catalog.Service{ID: "media/jellyfin", Source: catalog.SourceCompose, Name: "Jellyfin", Host: "vm", Project: "media"}
	for _, c := range cs {
		s.Containers = append(s.Containers, catalog.Container{Host: "vm", ID: "id-" + c, Name: c, State: "running"})
	}
	return s
}

func openStream(t *testing.T, ctx context.Context, c *http.Client, url string, lastID string) *sseReader {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, Content-Type %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	return newSSEReader(res.Body)
}

func TestStreamUnContenedor(t *testing.T) {
	fl := &fakeLogs{
		history: map[string][]agent.LogLine{"id-jellyfin": {line(1, "uno"), line(2, "dos")}},
		live:    map[string]chan agent.LogLine{"id-jellyfin": make(chan agent.LogLine)},
	}
	srv := testServerLogs(t, Snapshot{Services: []catalog.Service{svcWith("jellyfin")}}, fl)
	c := loggedIn(t, srv)

	r := openStream(t, context.Background(), c, srv.URL+"/logs/media/jellyfin", "")
	for _, want := range []string{"uno", "dos"} {
		if l := r.nextLine(t); l.Line != want || l.Container != "" {
			t.Errorf("línea %+v, quería %q sin contenedor", l, want)
		}
	}

	// Lo nuevo llega en vivo; la repetición de la última hora (since de
	// Docker es inclusivo) no se manda dos veces.
	fl.live["id-jellyfin"] <- line(3, "tres")
	if l := r.nextLine(t); l.Line != "tres" {
		t.Errorf("en vivo: %+v", l)
	}
	calls := fl.callsFor("id-jellyfin")
	if len(calls) != 2 || calls[0].opts.Follow || calls[0].opts.Tail != initialTail ||
		!calls[1].opts.Follow || !calls[1].opts.Since.Equal(t0.Add(2*time.Second)) {
		t.Errorf("pedidos al agent: %+v", calls)
	}

	// El contenedor se detiene: evento stopped, y el stream sigue abierto.
	close(fl.live["id-jellyfin"])
	if ev := r.next(t); ev.Event != "stopped" || !strings.Contains(ev.Data, "se detuvo") {
		t.Errorf("quería stopped, llegó %+v", ev)
	}
}

func TestStreamReplicasIntercaladas(t *testing.T) {
	var a, b []agent.LogLine
	for i := range 700 {
		a = append(a, line(2*i, fmt.Sprintf("a%d", i)))
		b = append(b, line(2*i+1, fmt.Sprintf("b%d", i)))
	}
	fl := &fakeLogs{
		history: map[string][]agent.LogLine{"id-a": a, "id-b": b},
		live:    map[string]chan agent.LogLine{"id-a": make(chan agent.LogLine), "id-b": make(chan agent.LogLine)},
	}
	srv := testServerLogs(t, Snapshot{Services: []catalog.Service{svcWith("a", "b")}}, fl)
	r := openStream(t, context.Background(), loggedIn(t, srv), srv.URL+"/logs/media/jellyfin", "")

	// 1400 líneas en total: llegan las últimas initialTail, en orden.
	var prev time.Time
	for i := range initialTail {
		l := r.nextLine(t)
		if l.TS.Before(prev) {
			t.Fatalf("línea %d fuera de orden: %v antes que %v", i, l.TS, prev)
		}
		prev = l.TS
		if i == 0 && l.Line != "a200" {
			t.Errorf("primera línea = %q, quería a200", l.Line)
		}
		if l.Container != "a" && l.Container != "b" {
			t.Fatalf("con réplicas, cada línea dice de qué contenedor es: %+v", l)
		}
	}
	fl.live["id-b"] <- line(5000, "b nueva")
	if l := r.nextLine(t); l.Line != "b nueva" || l.Container != "b" {
		t.Errorf("en vivo: %+v", l)
	}

	// Una réplica se detiene y la otra sigue.
	close(fl.live["id-a"])
	fl.live["id-b"] <- line(5001, "b sigue")
	if l := r.nextLine(t); l.Line != "b sigue" {
		t.Errorf("después de detenerse a: %+v", l)
	}
}

func TestStreamUnaReplica(t *testing.T) {
	fl := &fakeLogs{
		history: map[string][]agent.LogLine{"id-a": {line(1, "de a")}, "id-b": {line(2, "de b")}},
		live:    map[string]chan agent.LogLine{"id-b": make(chan agent.LogLine)},
	}
	srv := testServerLogs(t, Snapshot{Services: []catalog.Service{svcWith("a", "b")}}, fl)
	r := openStream(t, context.Background(), loggedIn(t, srv), srv.URL+"/logs/media/jellyfin?container=b", "")
	if l := r.nextLine(t); l.Line != "de b" || l.Container != "b" {
		t.Errorf("got %+v", l)
	}
	if n := len(fl.callsFor("id-a")); n != 0 {
		t.Errorf("se abrieron %d pedidos a la réplica no elegida", n)
	}
}

// Al reconectar con Last-Event-ID no se repite el historial: se sigue desde
// esa hora.
func TestStreamReconexion(t *testing.T) {
	fl := &fakeLogs{
		history: map[string][]agent.LogLine{"id-jellyfin": {line(1, "vieja"), line(2, "ya vista"), line(3, "perdida en el corte")}},
		live:    map[string]chan agent.LogLine{"id-jellyfin": make(chan agent.LogLine)},
	}
	srv := testServerLogs(t, Snapshot{Services: []catalog.Service{svcWith("jellyfin")}}, fl)
	last := t0.Add(2 * time.Second).Format(time.RFC3339Nano)
	r := openStream(t, context.Background(), loggedIn(t, srv), srv.URL+"/logs/media/jellyfin", last)

	if l := r.nextLine(t); l.Line != "perdida en el corte" {
		t.Errorf("primera línea al reconectar = %q", l.Line)
	}
	calls := fl.callsFor("id-jellyfin")
	if len(calls) != 1 || !calls[0].opts.Follow || calls[0].opts.Tail != agent.MaxLogTail {
		t.Errorf("pedidos: %+v", calls)
	}
}

func TestStreamReplicaQueFalla(t *testing.T) {
	fl := &fakeLogs{
		history: map[string][]agent.LogLine{"id-b": {line(1, "b anda")}},
		live:    map[string]chan agent.LogLine{"id-b": make(chan agent.LogLine)},
		openErr: map[string]error{"id-a": agent.ErrContainerNotFound},
	}
	srv := testServerLogs(t, Snapshot{Services: []catalog.Service{svcWith("a", "b")}}, fl)
	r := openStream(t, context.Background(), loggedIn(t, srv), srv.URL+"/logs/media/jellyfin", "")

	if ev := r.next(t); ev.Event != "notice" || !strings.Contains(ev.Data, "a: el contenedor ya no existe") {
		t.Errorf("quería un aviso por a, llegó %+v", ev)
	}
	if l := r.nextLine(t); l.Line != "b anda" {
		t.Errorf("got %+v", l)
	}
	// a vuelve a fallar al seguir: otro aviso, y b sigue.
	if ev := r.next(t); ev.Event != "notice" {
		t.Errorf("quería el aviso del follow de a, llegó %+v", ev)
	}
	close(fl.live["id-b"])
	if ev := r.next(t); ev.Event != "end" || !strings.Contains(ev.Data, "cortó") {
		t.Errorf("con un error, el fin no dice que se detuvo: %+v", ev)
	}
}

// Cerrar la pestaña corta los pedidos a los agents.
func TestStreamCorteDelNavegador(t *testing.T) {
	fl := &fakeLogs{
		history: map[string][]agent.LogLine{"id-a": {line(1, "x")}, "id-b": {line(2, "y")}},
		live:    map[string]chan agent.LogLine{"id-a": make(chan agent.LogLine), "id-b": make(chan agent.LogLine)},
	}
	srv := testServerLogs(t, Snapshot{Services: []catalog.Service{svcWith("a", "b")}}, fl)
	ctx, cancel := context.WithCancel(context.Background())
	r := openStream(t, ctx, loggedIn(t, srv), srv.URL+"/logs/media/jellyfin", "")
	r.nextLine(t)
	r.nextLine(t)
	waitFor(t, func() bool { return fl.abiertas.Load() == 2 }, "los dos follow abiertos")

	cancel()
	waitFor(t, func() bool { return fl.abiertas.Load() == 0 }, "que se cierren los pedidos al agent")
}

func TestStreamKeepalive(t *testing.T) {
	old := keepalive
	keepalive = 20 * time.Millisecond
	t.Cleanup(func() { keepalive = old })

	fl := &fakeLogs{live: map[string]chan agent.LogLine{"id-jellyfin": make(chan agent.LogLine)}}
	srv := testServerLogs(t, Snapshot{Services: []catalog.Service{svcWith("jellyfin")}}, fl)
	r := openStream(t, context.Background(), loggedIn(t, srv), srv.URL+"/logs/media/jellyfin", "")
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-r.ch:
			if ev.Comment == "keepalive" {
				return
			}
		case <-deadline:
			t.Fatal("no llegó ningún keepalive")
		}
	}
}

func TestStreamErrores(t *testing.T) {
	snap := Snapshot{Services: []catalog.Service{
		svcWith("jellyfin"),
		{ID: "router", Source: catalog.SourceExternal, Name: "Router"},
	}}
	srv := testServerLogs(t, snap, &fakeLogs{})
	c := loggedIn(t, srv)
	for path, want := range map[string]int{
		"/logs/no/existe":                     http.StatusNotFound,
		"/logs/router":                        http.StatusNotFound, // sin contenedores
		"/logs/media/jellyfin?container=otro": http.StatusNotFound,
	} {
		res, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("GET %s = %d, quería %d", path, res.StatusCode, want)
		}
	}

	// Sin sesión no hay logs.
	res, err := client(t).Get(srv.URL + "/logs/media/jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Errorf("sin sesión = %d, quería 303 al login", res.StatusCode)
	}
}

func TestPaginaDeServicio(t *testing.T) {
	snap := Snapshot{
		Hosts: []HostStatus{{Name: "vm", OK: true}},
		Services: []catalog.Service{
			svcWith("jellyfin"),
			{ID: "router", Source: catalog.SourceExternal, Name: "Router", URL: "http://192.168.0.1"},
		},
	}
	srv := testServerLogs(t, snap, &fakeLogs{})
	c := loggedIn(t, srv)

	res, _ := c.Get(srv.URL + "/")
	if b := body(t, res); !strings.Contains(b, `href="/services/media/jellyfin"`) {
		t.Error("la tarjeta de la portada no enlaza a la página del servicio")
	}

	res, _ = c.Get(srv.URL + "/services/media/jellyfin")
	b := body(t, res)
	for _, want := range []string{"Jellyfin", `data-stream="/logs/media/jellyfin"`, "/static/gen/logs.js", "Actividad", "compose · media"} {
		if !strings.Contains(b, want) {
			t.Errorf("falta %q en la página", want)
		}
	}
	if strings.Contains(b, "data-replicas") {
		t.Error("con un solo contenedor no hay selector de réplicas")
	}

	res, _ = c.Get(srv.URL + "/services/router")
	if b := body(t, res); !strings.Contains(b, "no tiene logs") || strings.Contains(b, "logs.js") {
		t.Error("un servicio externo no tiene visor de logs")
	}

	res, _ = c.Get(srv.URL + "/services/no-existe")
	if b := body(t, res); res.StatusCode != http.StatusNotFound || !strings.Contains(b, "no existe") {
		t.Errorf("servicio inexistente = %d", res.StatusCode)
	}
}

func TestEscapeID(t *testing.T) {
	for in, want := range map[string]string{
		"media-stack/jellyfin": "media-stack/jellyfin",
		"con espacio/y?":       "con%20espacio/y%3F",
		"suelto":               "suelto",
	} {
		if got := escapeID(in); got != want {
			t.Errorf("escapeID(%q) = %q, quería %q", in, got, want)
		}
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("pasaron 2 s esperando %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLimiter(t *testing.T) {
	l := limiter{max: 3}
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	var passed, reported int
	for i := range 10 { // 10 líneas en el primer segundo: pasan 3
		ok, sk := l.allow(at(i * 10))
		if ok {
			passed++
		}
		reported += sk
	}
	if passed != 3 || reported != 0 {
		t.Errorf("primer segundo: pasaron %d, avisadas %d", passed, reported)
	}
	if n := l.expired(at(500)); n != 0 {
		t.Errorf("antes de que termine la ventana no se avisa: %d", n)
	}
	// Llega una línea en la ventana siguiente: pasa, y trae el aviso de 7.
	if ok, sk := l.allow(at(1200)); !ok || sk != 7 {
		t.Errorf("ventana nueva: ok=%v avisadas=%d, quería true y 7", ok, sk)
	}
	for i := range 5 {
		l.allow(at(1300 + i))
	}
	// El contenedor se calla: el ticker avisa las 3 omitidas.
	if n := l.expired(at(2300)); n != 3 {
		t.Errorf("expired = %d, quería 3", n)
	}
	if n := l.expired(at(2400)); n != 0 {
		t.Errorf("se avisa una sola vez: %d", n)
	}
}

func TestStreamInundacion(t *testing.T) {
	old := maxRate
	maxRate = 5
	t.Cleanup(func() { maxRate = old })

	live := make(chan agent.LogLine, 100)
	fl := &fakeLogs{live: map[string]chan agent.LogLine{"id-jellyfin": live}}
	srv := testServerLogs(t, Snapshot{Services: []catalog.Service{svcWith("jellyfin")}}, fl)
	r := openStream(t, context.Background(), loggedIn(t, srv), srv.URL+"/logs/media/jellyfin", "")
	waitFor(t, func() bool { return fl.abiertas.Load() == 1 }, "el follow abierto")

	for i := range 20 {
		live <- line(100+i, fmt.Sprintf("l%d", i))
	}
	for range 5 {
		r.nextLine(t)
	}
	if ev := r.next(t); ev.Event != "notice" || !strings.Contains(ev.Data, "omitieron 15") {
		t.Errorf("quería el aviso de 15 omitidas, llegó %+v", ev)
	}
}

// mutableSnapshot es una foto que el test cambia mientras el stream corre.
type mutableSnapshot struct {
	mu   sync.Mutex
	snap Snapshot
}

func (m *mutableSnapshot) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snap
}

func (m *mutableSnapshot) set(s Snapshot) {
	m.mu.Lock()
	m.snap = s
	m.mu.Unlock()
}

func TestStreamSigueDespuesDeDetenerse(t *testing.T) {
	old := restartPoll
	restartPoll = 20 * time.Millisecond
	t.Cleanup(func() { restartPoll = old })

	started := t0
	svc := svcWith("jellyfin")
	svc.Containers[0].StartedAt = started
	snap := &mutableSnapshot{snap: Snapshot{Services: []catalog.Service{svc}}}
	fl := &fakeLogs{
		history: map[string][]agent.LogLine{"id-jellyfin": {line(1, "uno")}},
		live:    map[string]chan agent.LogLine{"id-jellyfin": make(chan agent.LogLine)},
	}
	live := make(chan store.StateEvent, 4)
	mon := &fakeMonitor{snap: snap.Snapshot(), live: live}

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, _ := auth.HashPassword("una-contraseña-larga")
	_ = st.SetPassword(context.Background(), "admin", hash, time.Now())
	logger := slog.New(slog.DiscardHandler)
	h, err := newHandler(snap, fl, nil, mon, st, auth.NewSessions(st, false, logger), logger)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := loggedIn(t, srv)

	r := openStream(t, context.Background(), c, srv.URL+"/logs/media/jellyfin", "")
	if l := r.nextLine(t); l.Line != "uno" {
		t.Fatalf("primera: %+v", l)
	}

	// Se detiene: stopped, y el cambio de estado llega igual en vivo.
	close(fl.live["id-jellyfin"])
	if ev := r.next(t); ev.Event != "stopped" {
		t.Fatalf("quería stopped, llegó %+v", ev)
	}
	live <- store.StateEvent{ID: 1, At: t0.Add(2 * time.Second), From: "up", To: "down", Reason: "jellyfin detenido (exit 137)"}
	if ev := r.next(t); ev.Event != "state" || !strings.Contains(ev.Data, "exit 137") {
		t.Fatalf("estado con el contenedor detenido: %+v", ev)
	}

	// Mientras la foto siga con el arranque viejo, no se retoma nada.
	time.Sleep(100 * time.Millisecond)
	if n := len(fl.callsFor("id-jellyfin")); n != 2 {
		t.Errorf("con la foto vieja se volvió a pedir logs: %d pedidos", n)
	}

	// Vuelve a arrancar (mismo contenedor, hora nueva): started y siguen
	// los logs desde la última línea mandada.
	fl.mu.Lock()
	fl.live["id-jellyfin"] = make(chan agent.LogLine)
	fl.mu.Unlock()
	svc.Containers = []catalog.Container{svc.Containers[0]}
	svc.Containers[0].StartedAt = started.Add(10 * time.Second)
	snap.set(Snapshot{Services: []catalog.Service{svc}})
	if ev := r.next(t); ev.Event != "started" {
		t.Fatalf("quería started, llegó %+v", ev)
	}
	fl.live["id-jellyfin"] <- line(11, "de vuelta")
	if l := r.nextLine(t); l.Line != "de vuelta" {
		t.Errorf("después de arrancar: %+v", l)
	}
	calls := fl.callsFor("id-jellyfin")
	if last := calls[len(calls)-1]; !last.opts.Follow || !last.opts.Since.Equal(t0.Add(time.Second)) {
		t.Errorf("tendría que seguir desde la última línea mandada: %+v", last.opts)
	}
}
