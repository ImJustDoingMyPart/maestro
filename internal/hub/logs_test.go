package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
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
			case l, ok := <-f.live[id]:
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

	// El contenedor se detiene: evento end y fin del stream.
	close(fl.live["id-jellyfin"])
	if ev := r.next(t); ev.Event != "end" || !strings.Contains(ev.Data, "se detuvo") {
		t.Errorf("quería end, llegó %+v", ev)
	}
	r.closed(t)
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
