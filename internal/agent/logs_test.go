package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeLogs da siempre las mismas líneas. Con Follow, después de darlas se
// queda esperando hasta que se cancele el contexto, como Docker. Cuenta las
// lecturas abiertas para ver que un corte del cliente las cierra.
type fakeLogs struct {
	lines   []LogLine
	openErr error // si no es nil, falla la apertura
	readErr error // si no es nil, sale del iterador después de las líneas

	abiertas atomic.Int32 // lecturas en curso
	mu       sync.Mutex
	got      LogOptions // las opciones del último pedido
	gotID    string
}

func (f *fakeLogs) open(ctx context.Context, id string, opts LogOptions) (iter.Seq2[LogLine, error], error) {
	f.mu.Lock()
	f.got, f.gotID = opts, id
	f.mu.Unlock()
	if f.openErr != nil {
		return nil, f.openErr
	}

	f.abiertas.Add(1)
	return func(yield func(LogLine, error) bool) {
		defer f.abiertas.Add(-1)
		for _, l := range f.lines {
			if !yield(l, nil) {
				return
			}
		}
		if f.readErr != nil {
			yield(LogLine{}, f.readErr)
			return
		}
		if opts.Follow {
			<-ctx.Done()
		}
	}, nil
}

func (f *fakeLogs) opts() (string, LogOptions) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotID, f.got
}

var testLines = []LogLine{
	{TS: time.Date(2026, 11, 10, 14, 3, 7, 123456789, time.UTC), Stream: "stdout", Line: "arrancando"},
	{TS: time.Date(2026, 11, 10, 14, 3, 8, 0, time.UTC), Stream: "stderr", Line: `level=error msg="sin \"base\""`},
	{Stream: "stdout", Line: ""},
}

func TestParseLogOptions(t *testing.T) {
	since := time.Date(2026, 11, 10, 14, 3, 7, 5, time.UTC)
	tests := []struct {
		q       string
		want    LogOptions
		wantErr bool
	}{
		{"", LogOptions{Tail: DefaultLogTail}, false},
		{"tail=0", LogOptions{Tail: 0}, false},
		{"tail=50&follow=1", LogOptions{Tail: 50, Follow: true}, false},
		{"tail=999999", LogOptions{Tail: MaxLogTail}, false},
		{"follow=true", LogOptions{Tail: DefaultLogTail, Follow: true}, false},
		{"follow=0", LogOptions{Tail: DefaultLogTail}, false},
		{"since=2026-11-10T14:03:07.000000005Z", LogOptions{Tail: DefaultLogTail, Since: since}, false},
		{"tail=-1", LogOptions{}, true},
		{"tail=diez", LogOptions{}, true},
		{"follow=quizas", LogOptions{}, true},
		{"since=ayer", LogOptions{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.q, func(t *testing.T) {
			q, _ := url.ParseQuery(tt.q)
			got, err := parseLogOptions(q)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, quería error: %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Lo que arma el cliente lo entiende el agent, ida y vuelta.
func TestLogOptionsIdaYVuelta(t *testing.T) {
	for _, o := range []LogOptions{
		{Tail: 0},
		{Tail: 100, Follow: true},
		{Tail: 10, Since: time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)},
	} {
		got, err := parseLogOptions(o.encode())
		if err != nil || !reflect.DeepEqual(got, o) {
			t.Errorf("ida y vuelta de %+v: got %+v, err %v", o, got, err)
		}
	}
}

func get(t *testing.T, ctx context.Context, rawURL string) *http.Response {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestLogsNDJSON(t *testing.T) {
	fl := &fakeLogs{lines: testLines}
	srv := newTestServer(t, fakeSource{logs: fl})

	res := get(t, context.Background(), srv.URL+"/v1/containers/abc123/logs?tail=7")
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q", ct)
	}

	// Una línea de texto por evento, cada una un objeto JSON.
	var got []LogLine
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		var l LogLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("línea %q: %v", sc.Text(), err)
		}
		got = append(got, l)
	}
	if !equalLines(got, testLines) {
		t.Errorf("líneas:\n got %+v\nwant %+v", got, testLines)
	}
	if id, o := fl.opts(); id != "abc123" || o != (LogOptions{Tail: 7}) {
		t.Errorf("la fuente recibió id=%q opts=%+v", id, o)
	}
}

func TestLogsErroresDeApertura(t *testing.T) {
	tests := []struct {
		name string
		src  fakeSource
		path string
		want int
	}{
		{"contenedor inexistente", fakeSource{}, "/v1/containers/nada/logs", http.StatusNotFound},
		{"tail inválido", fakeSource{logs: &fakeLogs{}}, "/v1/containers/a/logs?tail=x", http.StatusBadRequest},
		{"docker caído", fakeSource{logs: &fakeLogs{openErr: errors.New("sin socket")}}, "/v1/containers/a/logs", http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t, tt.src)
			res := get(t, context.Background(), srv.URL+tt.path)
			res.Body.Close()
			if res.StatusCode != tt.want {
				t.Errorf("status = %d, quería %d", res.StatusCode, tt.want)
			}
		})
	}
}

func TestLogsPideToken(t *testing.T) {
	srv := newTestServer(t, fakeSource{logs: &fakeLogs{lines: testLines}})
	res, err := http.Get(srv.URL + "/v1/containers/a/logs")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, quería 401", res.StatusCode)
	}
}

// Si el hub corta un stream con follow, el agent deja de leer de Docker.
func TestLogsCorteDelCliente(t *testing.T) {
	fl := &fakeLogs{lines: testLines}
	srv := newTestServer(t, fakeSource{logs: fl})

	ctx, cancel := context.WithCancel(context.Background())
	res := get(t, ctx, srv.URL+"/v1/containers/a/logs?follow=1")
	defer res.Body.Close()

	// Las líneas llegan sin esperar al final del stream (que no llega nunca).
	sc := bufio.NewScanner(res.Body)
	for range testLines {
		if !sc.Scan() {
			t.Fatalf("stream cortado antes de tiempo: %v", sc.Err())
		}
	}
	if n := fl.abiertas.Load(); n != 1 {
		t.Fatalf("lecturas abiertas con el stream vivo = %d, quería 1", n)
	}

	cancel()
	waitFor(t, func() bool { return fl.abiertas.Load() == 0 }, "que el agent cierre la lectura")
}

// Un error a mitad del stream no puede cambiar el 200 ya mandado: corta el
// cuerpo, y el cliente lo ve como fin.
func TestLogsErrorDeLectura(t *testing.T) {
	fl := &fakeLogs{lines: testLines[:1], readErr: errors.New("docker se cayó")}
	srv := newTestServer(t, fakeSource{logs: fl})

	lines, err := NewClient(srv.URL, testToken).Logs(context.Background(), "a", LogOptions{Tail: 1})
	if err != nil {
		t.Fatal(err)
	}
	var got []LogLine
	for l, err := range lines {
		if err != nil {
			t.Fatalf("error inesperado: %v", err)
		}
		got = append(got, l)
	}
	if !equalLines(got, testLines[:1]) {
		t.Errorf("got %+v", got)
	}
}

func TestClienteLogs(t *testing.T) {
	fl := &fakeLogs{lines: testLines}
	srv := newTestServer(t, fakeSource{logs: fl})
	c := NewClient(srv.URL, testToken)

	opts := LogOptions{Tail: 3, Since: time.Date(2026, 11, 10, 0, 0, 0, 1, time.UTC)}
	lines, err := c.Logs(context.Background(), "media/jellyfin", opts)
	if err != nil {
		t.Fatal(err)
	}
	var got []LogLine
	for l, err := range lines {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, l)
	}
	if !equalLines(got, testLines) {
		t.Errorf("líneas:\n got %+v\nwant %+v", got, testLines)
	}
	if id, o := fl.opts(); id != "media/jellyfin" || !reflect.DeepEqual(o, opts) {
		t.Errorf("la fuente recibió id=%q opts=%+v", id, o)
	}

	if _, err := c.Logs(context.Background(), "a", LogOptions{}); err != nil {
		t.Errorf("segundo pedido: %v", err)
	}
	if _, err := NewClient(srv.URL, testToken).Logs(context.Background(), "a", LogOptions{Tail: -1}); err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("tail negativo: err = %v, quería un 400", err)
	}

	sinLogs := newTestServer(t, fakeSource{})
	if _, err := NewClient(sinLogs.URL, testToken).Logs(context.Background(), "x", LogOptions{}); !errors.Is(err, ErrContainerNotFound) {
		t.Errorf("contenedor inexistente: err = %v, quería ErrContainerNotFound", err)
	}
}

// Cortar el range del lado del cliente cierra la conexión y el agent deja
// de leer.
func TestClienteLogsCorteDelRange(t *testing.T) {
	fl := &fakeLogs{lines: testLines}
	srv := newTestServer(t, fakeSource{logs: fl})

	lines, err := NewClient(srv.URL, testToken).Logs(context.Background(), "a", LogOptions{Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range lines {
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	waitFor(t, func() bool { return fl.abiertas.Load() == 0 }, "que el agent cierre la lectura")
}

// equalLines compara con time.Equal: el JSON no conserva la *Location.
func equalLines(a, b []LogLine) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].TS.Equal(b[i].TS) || a[i].Stream != b[i].Stream || a[i].Line != b[i].Line {
			return false
		}
	}
	return true
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
