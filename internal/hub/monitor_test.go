package hub

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

func openStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "m.db")
	st, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, path
}

func newTestMonitor(t *testing.T, st *store.Store) (*Monitor, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	m, err := NewMonitor(ctx, st, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); m.runner.Wait() })
	return m, cancel
}

// waitEvent espera un cambio de estado por la suscripción.
func waitEvent(t *testing.T, ch <-chan store.StateEvent) store.StateEvent {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no llegó el cambio de estado")
		return store.StateEvent{}
	}
}

func TestMonitorCheckCaeYVuelve(t *testing.T) {
	var code atomic.Int32
	code.Store(200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(code.Load()))
	}))
	defer srv.Close()

	st, _ := openStore(t)
	m, _ := newTestMonitor(t, st)
	const id = "web/app"
	events, unsub := m.Subscribe(id)
	defer unsub()

	snap := Snapshot{
		Hosts: []HostStatus{{Name: "vm", OK: true}},
		Services: []catalog.Service{{
			ID: id, Source: catalog.SourceCompose, Host: "vm",
			Containers: []catalog.Container{{ID: "c1", Name: "app", State: "running", Host: "vm"}},
			Checks:     []catalog.Check{{Kind: catalog.CheckHTTP, Target: srv.URL, Interval: 30 * time.Millisecond}},
		}},
	}
	m.Update(snap)

	// Primera evaluación: arriba por el contenedor (el check todavía no
	// corrió).
	if e := waitEvent(t, events); e.From != "" || e.To != "up" || e.Reason != "arriba" {
		t.Errorf("primer evento: %+v", e)
	}

	code.Store(503)
	e := waitEvent(t, events)
	if e.To != "degraded" || e.Reason != "check http falla: código 503 (esperado 2xx)" {
		t.Errorf("primera falla: %+v", e)
	}
	e = waitEvent(t, events)
	if e.From != "degraded" || e.To != "down" || e.Reason != "check http falla: código 503 (esperado 2xx)" {
		t.Errorf("tercera falla: %+v", e)
	}
	if got := m.Status(id); got.State != "down" {
		t.Errorf("Status = %+v", got)
	}
	cv := m.Checks(id)
	if len(cv) != 1 || cv[0].Failures < 3 || cv[0].Last.Detail != "código 503 (esperado 2xx)" {
		t.Errorf("Checks = %+v", cv)
	}

	code.Store(200)
	if e := waitEvent(t, events); e.To != "up" || e.Reason != "volvió a responder" {
		t.Errorf("vuelta: %+v", e)
	}

	// Todo quedó en la base, en orden.
	results, stored, err := m.History(context.Background(), id, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 4 { // 3 fallas y la vuelta, como mínimo
		t.Errorf("solo %d resultados guardados", len(results))
	}
	var seq []string
	for _, e := range stored {
		seq = append(seq, e.To)
	}
	if want := []string{"up", "degraded", "down", "up"}; len(seq) != 4 || seq[0] != want[0] || seq[1] != want[1] || seq[2] != want[2] || seq[3] != want[3] {
		t.Errorf("eventos guardados = %v, quería %v", seq, want)
	}
}

func TestMonitorReinicio(t *testing.T) {
	st, path := openStore(t)
	m, cancel := newTestMonitor(t, st)
	const id = "web/app"
	t0 := time.Now().Add(-time.Hour)
	snap := func(started time.Time, exit int) Snapshot {
		return Snapshot{
			Hosts: []HostStatus{{Name: "vm", OK: true}},
			Services: []catalog.Service{{
				ID: id, Source: catalog.SourceCompose, Host: "vm",
				Containers: []catalog.Container{{ID: "c1", Name: "app", State: "running", Host: "vm", StartedAt: started, ExitCode: exit}},
			}},
		}
	}
	m.Update(snap(t0, 0))
	if s := m.Status(id); s.State != "up" {
		t.Fatalf("Status = %+v", s)
	}
	m.Update(snap(t0, 0)) // sin cambios: nada
	m.Update(snap(time.Now(), 137))
	if s := m.Status(id); s.State != "degraded" || s.Reason() != "app reiniciado (exit 137)" {
		t.Errorf("después del reinicio: %+v", s)
	}

	// Pasada la ventana, vuelve a arriba.
	m.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	m.Update(snap(time.Now(), 137))
	if s := m.Status(id); s.State != "up" {
		t.Errorf("pasada la ventana: %+v", s)
	}

	// Un hub nuevo sobre la misma base sigue donde quedó: el mismo estado
	// no es un evento nuevo.
	cancel()
	m.runner.Wait()
	st.Close()
	st2, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	m2, _ := newTestMonitor(t, st2)
	m2.Update(snap(time.Now(), 137))
	_, evs, _ := m2.History(context.Background(), id, time.Time{})
	if len(evs) != 3 {
		t.Errorf("eventos = %+v, quería 3 (up, degraded, up)", evs)
	}
}

func TestMonitorPush(t *testing.T) {
	st, _ := openStore(t)
	m, _ := newTestMonitor(t, st)
	const token = "0123456789abcdef"
	events, unsub := m.Subscribe("restic")
	defer unsub()
	m.Update(Snapshot{Services: []catalog.Service{{
		ID: "restic", Source: catalog.SourceExternal,
		Checks: []catalog.Check{{Kind: catalog.CheckPush, Target: token, Interval: 100 * time.Millisecond}},
	}}})
	if s := m.Status("restic"); s.State != "unknown" {
		t.Errorf("sin pushes todavía: %+v", s)
	}
	if m.Push("no-es-el-token-0000") {
		t.Error("Push aceptó un token ajeno")
	}
	if !m.Push(token) {
		t.Fatal("Push no reconoció el token")
	}
	if e := waitEvent(t, events); e.To != "up" {
		t.Errorf("después del push: %+v", e)
	}
	if e := waitEvent(t, events); e.To != "down" || e.Reason != "push: no llegó en 100ms" {
		t.Errorf("sin push a tiempo: %+v", e)
	}
	if cv := m.Checks("restic"); len(cv) != 1 || cv[0].Target != "" {
		t.Errorf("Checks no tiene que mostrar el token: %+v", cv)
	}
}

func TestMonitorAgentCaidoNoEsEvento(t *testing.T) {
	st, _ := openStore(t)
	m, _ := newTestMonitor(t, st)
	svc := catalog.Service{ID: "a", Source: catalog.SourceContainer, Host: "vm",
		Containers: []catalog.Container{{ID: "c", Name: "a", State: "running", Host: "vm"}}}
	m.Update(Snapshot{Hosts: []HostStatus{{Name: "vm", OK: true}}, Services: []catalog.Service{svc}})
	m.Update(Snapshot{Hosts: []HostStatus{{Name: "vm", OK: false}}, Services: []catalog.Service{svc}})
	if s := m.Status("a"); s.State != "unknown" {
		t.Errorf("con el agent caído: %+v", s)
	}
	m.Update(Snapshot{Hosts: []HostStatus{{Name: "vm", OK: true}}, Services: []catalog.Service{svc}})
	_, evs, _ := m.History(context.Background(), "a", time.Time{})
	if len(evs) != 1 {
		t.Errorf("eventos = %+v, quería solo el primero", evs)
	}
}
