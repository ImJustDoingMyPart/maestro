package store

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func TestPermisos(t *testing.T) {
	s, path := openTest(t)
	// Escribir algo para que existan el -wal y el -shm.
	if err := s.AddCheckResult(context.Background(), CheckResult{Service: "a", Kind: "http", At: t0}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s: permisos %o, quería 600", filepath.Base(p), perm)
		}
	}

	// Una base que ya existía con 644 queda en 600.
	old := filepath.Join(t.TempDir(), "vieja.db")
	if err := os.WriteFile(old, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(context.Background(), old)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
	if info, _ := os.Stat(old); info.Mode().Perm() != 0o600 {
		t.Errorf("base vieja: permisos %o, quería 600", info.Mode().Perm())
	}
}

func TestCheckResults(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	add := func(r CheckResult) {
		t.Helper()
		if err := s.AddCheckResult(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	cert := t0.Add(30 * 24 * time.Hour)
	add(CheckResult{Service: "a", Kind: "http", At: t0, OK: true, Latency: 12 * time.Millisecond, Detail: "código 200", CertExpiry: cert})
	add(CheckResult{Service: "a", Kind: "http", At: t0.Add(time.Minute), Detail: "timeout (10s)"})
	add(CheckResult{Service: "a", Kind: "http", At: t0.Add(2 * time.Minute), Detail: "timeout (10s)"})
	add(CheckResult{Service: "a", Kind: "tcp", At: t0.Add(time.Minute), OK: true, Detail: "conectó"})
	add(CheckResult{Service: "b", Kind: "push", At: t0, OK: true, Detail: "llegó"})

	got, err := s.CheckResults(ctx, "a", t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Kind != "http" || got[2].At != t0.Add(2*time.Minute).Local() {
		t.Errorf("CheckResults desde t0+1m: %+v", got)
	}

	all, _ := s.CheckResults(ctx, "a", t0)
	if !all[0].CertExpiry.Equal(cert) || all[0].Latency != 12*time.Millisecond || !all[0].OK {
		t.Errorf("primer resultado: %+v", all[0])
	}
	if !all[1].CertExpiry.IsZero() {
		t.Errorf("sin certificado tiene que quedar cero: %v", all[1].CertExpiry)
	}

	latest, err := s.LatestCheckResults(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, r := range latest {
		keys = append(keys, r.Service+"/"+r.Kind+"@"+r.At.UTC().Format("15:04"))
	}
	want := []string{"a/http@12:02", "a/http@12:01", "a/tcp@12:01", "b/push@12:00"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("LatestCheckResults(2) = %v, quería %v", keys, want)
	}

	if at, err := s.LastOK(ctx, "a", "http"); err != nil || !at.Equal(t0) {
		t.Errorf("LastOK = %v, %v", at, err)
	}
	if at, err := s.LastOK(ctx, "z", "push"); err != nil || !at.IsZero() {
		t.Errorf("LastOK sin resultados = %v, %v", at, err)
	}
}

func TestCompact(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	// Hora 10: dos buenos y uno malo; hora 11: uno bueno; hora 12: uno
	// bueno que no se compacta.
	for _, r := range []CheckResult{
		{At: t0.Add(-2*time.Hour + time.Minute), OK: true, Latency: 10 * time.Millisecond},
		{At: t0.Add(-2*time.Hour + 2*time.Minute), OK: true, Latency: 20 * time.Millisecond},
		{At: t0.Add(-2*time.Hour + 3*time.Minute), Latency: 30 * time.Millisecond},
		{At: t0.Add(-time.Hour + 5*time.Minute), OK: true, Latency: 40 * time.Millisecond},
		{At: t0.Add(time.Minute), OK: true},
	} {
		r.Service, r.Kind = "a", "http"
		if err := s.AddCheckResult(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	// keepFrom a mitad de la hora 12: corta a las 12:00.
	n, err := s.Compact(ctx, t0.Add(30*time.Minute), t0.Add(-48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("compactó %d, quería 4", n)
	}
	left, _ := s.CheckResults(ctx, "a", time.Time{})
	if len(left) != 1 {
		t.Errorf("quedaron %d crudos, quería 1", len(left))
	}
	h, err := s.HourlySummaries(ctx, "a", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 || h[0].Total != 3 || h[0].OK != 2 || h[0].AvgLatency != 20*time.Millisecond ||
		!h[0].Hour.Equal(t0.Add(-2*time.Hour)) || h[1].Total != 1 {
		t.Errorf("resúmenes: %+v", h)
	}

	// Un resultado tardío de una hora ya compactada se suma, no pisa.
	if err := s.AddCheckResult(ctx, CheckResult{Service: "a", Kind: "http", At: t0.Add(-time.Hour), OK: true, Latency: 60 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Compact(ctx, t0, t0.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	h, _ = s.HourlySummaries(ctx, "a", time.Time{})
	if h[1].Total != 2 || h[1].OK != 2 || h[1].AvgLatency != 50*time.Millisecond {
		t.Errorf("hora 11 después de sumar: %+v", h[1])
	}

	// Los resúmenes viejos se borran.
	if _, err := s.Compact(ctx, t0, t0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	h, _ = s.HourlySummaries(ctx, "a", time.Time{})
	if len(h) != 1 {
		t.Errorf("después de borrar los viejos quedan %d resúmenes, quería 1", len(h))
	}
}

func TestStateEvents(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for i, e := range []StateEvent{
		{Service: "a", At: t0, From: "", To: "up", Reason: "arriba"},
		{Service: "a", At: t0.Add(time.Minute), From: "up", To: "down", Reason: "check http falla: timeout (10s)"},
		{Service: "b", At: t0, From: "", To: "degraded", Reason: "b unhealthy"},
		{Service: "a", At: t0.Add(2 * time.Minute), From: "down", To: "up", Reason: "volvió a responder"},
	} {
		id, err := s.AddStateEvent(ctx, e)
		if err != nil || id != int64(i+1) {
			t.Fatalf("AddStateEvent: %d, %v", id, err)
		}
	}
	last, err := s.LastStateEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(last) != 2 || last[0].Service != "a" || last[0].Reason != "volvió a responder" || last[1].To != "degraded" {
		t.Errorf("LastStateEvents: %+v", last)
	}
	evs, err := s.StateEvents(ctx, "a", t0.Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].To != "down" || evs[1].To != "up" {
		t.Errorf("StateEvents desde t0+1m: %+v", evs)
	}
	evs, _ = s.StateEvents(ctx, "a", time.Time{}, 1)
	if len(evs) != 1 || evs[0].To != "up" || evs[0].From != "down" {
		t.Errorf("StateEvents con límite 1 tiene que dar el último: %+v", evs)
	}
}
