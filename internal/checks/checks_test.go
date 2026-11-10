package checks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
)

func httpSpec(target string) Spec {
	return Spec{Service: "s", Check: catalog.Check{Kind: catalog.CheckHTTP, Target: target, Timeout: time.Second}}
}

func TestProbeHTTP(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != "Maestro-check/1" {
			t.Errorf("User-Agent = %q", r.UserAgent())
		}
	})
	mux.HandleFunc("/caido", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	mux.HandleFunc("/creado", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) })
	mux.HandleFunc("/vuelta", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", http.StatusFound) })
	mux.HandleFunc("/lento", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tests := []struct {
		name   string
		spec   Spec
		ok     bool
		detail string
	}{
		{"200", httpSpec(srv.URL + "/ok"), true, "código 200"},
		{"503", httpSpec(srv.URL + "/caido"), false, "código 503 (esperado 2xx)"},
		{"201 con expect 200", func() Spec { s := httpSpec(srv.URL + "/creado"); s.Expect = 200; return s }(), false, "código 201 (esperado 200)"},
		{"201 sin expect", httpSpec(srv.URL + "/creado"), true, "código 201"},
		{"sigue la redirección", httpSpec(srv.URL + "/vuelta"), true, "código 200"},
		{"sin redirecciones, 302 falla", func() Spec { s := httpSpec(srv.URL + "/vuelta"); s.NoRedirects = true; return s }(), false, "código 302 (esperado 2xx)"},
		{"sin redirecciones, expect 302", func() Spec {
			s := httpSpec(srv.URL + "/vuelta")
			s.NoRedirects, s.Expect = true, 302
			return s
		}(), true, "código 302"},
		{"timeout", func() Spec { s := httpSpec(srv.URL + "/lento"); s.Timeout = 100 * time.Millisecond; return s }(), false, "timeout (100ms)"},
		{"no resuelve", httpSpec("http://no-existe.invalid/"), false, "no resuelve no-existe.invalid"},
		{"conexión rechazada", httpSpec("http://" + closedAddr(t) + "/"), false, "conexión rechazada"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := newHTTPProber(tt.spec).probe(context.Background())
			if o.ok != tt.ok || o.detail != tt.detail {
				t.Errorf("probe = ok %v, %q; quería ok %v, %q", o.ok, o.detail, tt.ok, tt.detail)
			}
		})
	}
}

func TestProbeHTTPS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	// Sin confiar en la CA de prueba: falla y lo dice.
	o := newHTTPProber(httpSpec(srv.URL)).probe(context.Background())
	if o.ok || !strings.Contains(o.detail, "certificado") {
		t.Errorf("CA desconocida: ok %v, %q", o.ok, o.detail)
	}

	// Confiando: da el vencimiento del certificado.
	p := newHTTPProber(httpSpec(srv.URL))
	p.client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: srvPool(srv)}
	o = p.probe(context.Background())
	if !o.ok {
		t.Fatalf("con la CA de prueba: %q", o.detail)
	}
	if want := srv.Certificate().NotAfter; !o.certExpiry.Equal(want) {
		t.Errorf("certExpiry = %v, quería %v", o.certExpiry, want)
	}
}

func srvPool(srv *httptest.Server) *x509.CertPool {
	return srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
}

func TestProbeTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	spec := Spec{Check: catalog.Check{Kind: catalog.CheckTCP, Target: ln.Addr().String()}}
	if o := probeTCP(context.Background(), spec); !o.ok || o.detail != "conectó" {
		t.Errorf("puerto abierto: %+v", o)
	}
	spec.Target = closedAddr(t)
	if o := probeTCP(context.Background(), spec); o.ok || o.detail != "conexión rechazada" {
		t.Errorf("puerto cerrado: %+v", o)
	}
}

// closedAddr da una dirección local donde nadie escucha.
func closedAddr(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestDefaults(t *testing.T) {
	s := Spec{}
	if s.interval() != DefaultInterval || s.timeout() != DefaultTimeout {
		t.Errorf("por defecto: %v, %v", s.interval(), s.timeout())
	}
	s.Interval = 5 * time.Second
	if s.timeout() != 5*time.Second {
		t.Errorf("el timeout no pasa del intervalo: %v", s.timeout())
	}
	if got := formatDuration(96 * time.Hour); got != "96h" {
		t.Errorf("formatDuration(96h) = %q", got)
	}
	if got := formatDuration(90 * time.Minute); got != "1h30m" {
		t.Errorf("formatDuration(90m) = %q", got)
	}
	if got := formatDuration(30 * time.Second); got != "30s" {
		t.Errorf("formatDuration(30s) = %q", got)
	}
}

// collector junta los resultados que manda el Runner.
type collector struct {
	mu  sync.Mutex
	got []Result
	ch  chan Result
}

func newCollector() *collector { return &collector{ch: make(chan Result, 100)} }

func (c *collector) sink(r Result) {
	c.mu.Lock()
	c.got = append(c.got, r)
	c.mu.Unlock()
	c.ch <- r
}

func (c *collector) next(t *testing.T) Result {
	t.Helper()
	select {
	case r := <-c.ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("no llegó ningún resultado")
		return Result{}
	}
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRunnerSync(t *testing.T) {
	var hits sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Store(r.URL.Path, true)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := newCollector()
	r := NewRunner(ctx, Options{Sink: c.sink, Logger: discard()})
	spec := Spec{Service: "a", Check: catalog.Check{Kind: catalog.CheckHTTP, Target: srv.URL + "/a", Interval: 50 * time.Millisecond}}
	r.Sync([]Spec{spec})

	res := c.next(t)
	if res.Service != "a" || res.Kind != catalog.CheckHTTP || !res.OK {
		t.Errorf("resultado: %+v", res)
	}
	c.next(t) // sigue corriendo por intervalo

	// Cambia la URL: se reinicia con la nueva.
	spec.Target = srv.URL + "/b"
	r.Sync([]Spec{spec})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := hits.Load("/b"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no se reinició con la URL nueva")
		}
		c.next(t)
	}

	// Sin specs: se para.
	r.Sync(nil)
	r.mu.Lock()
	n := len(r.jobs)
	r.mu.Unlock()
	if n != 0 {
		t.Errorf("quedaron %d checks corriendo", n)
	}
	cancel()
	r.Wait()
}

func TestRunnerPush(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCollector()
	const token = "0123456789abcdef"
	r := NewRunner(ctx, Options{Sink: c.sink, Logger: discard()})
	r.Sync([]Spec{{Service: "restic", Check: catalog.Check{Kind: catalog.CheckPush, Target: token, Interval: 200 * time.Millisecond}}})

	if r.Push("otro-token-cualquiera", time.Now()) {
		t.Error("Push aceptó un token que no es")
	}
	if r.Push(token[:8], time.Now()) {
		t.Error("Push aceptó un prefijo del token")
	}
	at := time.Now()
	if !r.Push(token, at) {
		t.Fatal("Push no reconoció el token")
	}
	res := c.next(t)
	if !res.OK || !res.At.Equal(at) || res.Service != "restic" || res.Kind != catalog.CheckPush {
		t.Errorf("push bueno: %+v", res)
	}

	// Sin más llamadas: a los 220 ms (intervalo + 10 %) falla.
	res = c.next(t)
	if res.OK || res.Detail != "no llegó en 200ms" {
		t.Errorf("push atrasado: %+v", res)
	}
	if late := res.At.Sub(at); late < 220*time.Millisecond {
		t.Errorf("falló a los %v, antes del intervalo más la gracia", late)
	}
	// Y sigue fallando una vez por intervalo.
	if res = c.next(t); res.OK {
		t.Errorf("segunda falla: %+v", res)
	}
}

func TestRunnerPushDesdeElUltimoGuardado(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCollector()
	key := Key{Service: "restic", Kind: catalog.CheckPush}
	r := NewRunner(ctx, Options{Sink: c.sink, Logger: discard(), LastPush: func(k Key) time.Time {
		if k != key {
			t.Errorf("LastPush(%v)", k)
		}
		return time.Now().Add(-time.Hour) // ya atrasado
	}})
	start := time.Now()
	r.Sync([]Spec{{Service: "restic", Check: catalog.Check{Kind: catalog.CheckPush, Target: "0123456789abcdef", Interval: 10 * time.Minute}}})
	res := c.next(t)
	if res.OK || time.Since(start) > time.Second {
		t.Errorf("un push atrasado desde antes de arrancar tiene que fallar enseguida: %+v", res)
	}
}

func TestRunnerPanicNoTiraElProceso(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	var calls int
	var mu sync.Mutex
	done := make(chan struct{})
	r := NewRunner(ctx, Options{Logger: discard(), Sink: func(Result) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 2 {
			close(done)
		}
		panic("sink roto")
	}})
	r.Sync([]Spec{{Service: "a", Check: catalog.Check{Kind: catalog.CheckHTTP, Target: srv.URL, Interval: 20 * time.Millisecond}}})
	select {
	case <-done: // pasó el pánico del primero y siguió
	case <-time.After(5 * time.Second):
		t.Fatal("el check no siguió después del pánico")
	}
}
