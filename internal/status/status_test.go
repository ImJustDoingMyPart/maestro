package status

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)

func running(name string) Container { return Container{Name: name, State: "running"} }

func up() Status { return Status{State: Up} }

func exit(n int) *int { return &n }

func TestEvaluate(t *testing.T) {
	p := DefaultParams
	tests := []struct {
		name string
		in   Input
		want Status
	}{
		{
			name: "contenedor corriendo sin checks",
			in:   Input{FromContainers: true, HostOK: true, Containers: []Container{running("a")}},
			want: up(),
		},
		{
			name: "contenedor detenido",
			in: Input{FromContainers: true, HostOK: true, Containers: []Container{
				{Name: "a", State: "exited", ExitCode: 137},
			}},
			want: Status{Down, []string{"a detenido (exit 137)"}},
		},
		{
			name: "reiniciándose, pausado y muerto",
			in: Input{FromContainers: true, HostOK: true, Containers: []Container{
				{Name: "a", State: "restarting", ExitCode: 1},
				{Name: "b", State: "paused"},
				{Name: "c", State: "dead"},
				running("d"),
			}},
			want: Status{Down, []string{"a reiniciándose (exit 1)", "b: paused", "c: dead"}},
		},
		{
			name: "unhealthy degrada",
			in: Input{FromContainers: true, HostOK: true, Containers: []Container{
				{Name: "a", State: "running", Health: "unhealthy"},
			}},
			want: Status{Degraded, []string{"a unhealthy"}},
		},
		{
			name: "reinicio reciente degrada; uno viejo no",
			in: Input{FromContainers: true, HostOK: true, Containers: []Container{
				{Name: "a", State: "running", RestartExit: exit(137), RestartedAt: now.Add(-9 * time.Minute)},
				{Name: "b", State: "running", RestartExit: exit(1), RestartedAt: now.Add(-11 * time.Minute)},
				{Name: "c", State: "running", RestartedAt: now.Add(-time.Minute)},
			}},
			want: Status{Degraded, []string{"a reiniciado (exit 137)", "c reiniciado"}},
		},
		{
			name: "unhealthy antes que reiniciado en el mismo contenedor",
			in: Input{FromContainers: true, HostOK: true, Containers: []Container{
				{Name: "a", State: "running", Health: "unhealthy", RestartExit: exit(2), RestartedAt: now.Add(-time.Minute)},
			}},
			want: Status{Degraded, []string{"a unhealthy", "a reiniciado (exit 2)"}},
		},
		{
			name: "reiniciado pero detenido: cuenta la caída",
			in: Input{FromContainers: true, HostOK: true, Containers: []Container{
				{Name: "a", State: "exited", ExitCode: 1, RestartedAt: now.Add(-time.Minute)},
			}},
			want: Status{Down, []string{"a detenido (exit 1)"}},
		},
		{
			name: "starting sin otros problemas: sin datos",
			in: Input{FromContainers: true, HostOK: true, Containers: []Container{
				{Name: "a", State: "running", Health: "starting"},
				running("b"),
			}},
			want: Status{Unknown, []string{"a arrancando"}},
		},
		{
			name: "starting con otro degradado: degradado",
			in: Input{FromContainers: true, HostOK: true, Containers: []Container{
				{Name: "a", State: "running", Health: "starting"},
				{Name: "b", State: "running", Health: "unhealthy"},
			}},
			want: Status{Degraded, []string{"b unhealthy"}},
		},
		{
			name: "check bajo el umbral degrada",
			in: Input{FromContainers: true, HostOK: true, Containers: []Container{running("a")},
				Checks: []Check{{Kind: "http", Results: 5, Failures: 2, Detail: "timeout"}}},
			want: Status{Degraded, []string{"check http falla: timeout"}},
		},
		{
			name: "check en el umbral tira",
			in: Input{FromContainers: true, HostOK: true, Containers: []Container{running("a")},
				Checks: []Check{{Kind: "http", Results: 5, Failures: 3, Detail: "código 503 (esperado 2xx)"}}},
			want: Status{Down, []string{"check http falla: código 503 (esperado 2xx)"}},
		},
		{
			name: "contenedor caído y check caído: primero el contenedor",
			in: Input{FromContainers: true, HostOK: true,
				Containers: []Container{{Name: "a", State: "exited"}},
				Checks: []Check{
					{Kind: "http", Results: 4, Failures: 4, Detail: "conexión rechazada"},
					{Kind: "tcp", Results: 4, Failures: 1, Detail: "timeout"},
				}},
			want: Status{Down, []string{"a detenido (exit 0)", "check http falla: conexión rechazada"}},
		},
		{
			name: "push con una falla tira",
			in:   Input{Checks: []Check{{Kind: "push", Results: 1, Failures: 1, Detail: "no llegó en 96h"}}},
			want: Status{Down, []string{"push: no llegó en 96h"}},
		},
		{
			name: "externo con check bueno",
			in:   Input{Checks: []Check{{Kind: "tcp", Results: 1}}},
			want: up(),
		},
		{
			name: "externo sin resultados todavía",
			in:   Input{Checks: []Check{{Kind: "http"}}},
			want: Status{Unknown, []string{"sin datos todavía"}},
		},
		{
			name: "check sin resultados se ignora aunque tenga fallas",
			in: Input{FromContainers: true, HostOK: true, Containers: []Container{running("a")},
				Checks: []Check{{Kind: "http", Failures: 5, Detail: "x"}}},
			want: up(),
		},
		{
			name: "agent caído sin checks: sin datos del agent",
			in: Input{FromContainers: true, HostOK: false, Containers: []Container{
				{Name: "a", State: "exited"},
			}},
			want: Status{Unknown, []string{"sin datos del agent"}},
		},
		{
			name: "agent caído con check bueno: arriba por el check",
			in: Input{FromContainers: true, HostOK: false,
				Containers: []Container{{Name: "a", State: "exited"}},
				Checks:     []Check{{Kind: "http", Results: 3}}},
			want: up(),
		},
		{
			name: "agent caído con check caído: caído",
			in: Input{FromContainers: true, HostOK: false,
				Containers: []Container{running("a")},
				Checks:     []Check{{Kind: "tcp", Results: 3, Failures: 3, Detail: "timeout"}}},
			want: Status{Down, []string{"check tcp falla: timeout"}},
		},
		{
			name: "sin contenedores",
			in:   Input{FromContainers: true, HostOK: true},
			want: Status{Unknown, []string{"sin contenedores"}},
		},
		{
			name: "certificado por vencer",
			in: Input{Checks: []Check{
				{Kind: "http", Results: 1, CertExpiry: now.Add(13 * 24 * time.Hour)},
			}},
			want: Status{Degraded, []string{"certificado vence el 2026-11-23"}},
		},
		{
			name: "certificado lejos",
			in: Input{Checks: []Check{
				{Kind: "http", Results: 1, CertExpiry: now.Add(15 * 24 * time.Hour)},
			}},
			want: up(),
		},
		{
			name: "fecha del certificado en UTC",
			in: Input{Checks: []Check{
				{Kind: "http", Results: 1, CertExpiry: time.Date(2026, 11, 20, 23, 30, 0, 0, time.FixedZone("-03", -3*3600))},
			}},
			want: Status{Degraded, []string{"certificado vence el 2026-11-21"}},
		},
		{
			name: "check fallando no da motivo de certificado",
			in: Input{Checks: []Check{
				{Kind: "http", Results: 2, Failures: 1, Detail: "timeout", CertExpiry: now.Add(24 * time.Hour)},
			}},
			want: Status{Degraded, []string{"check http falla: timeout"}},
		},
		{
			name: "contenedor degradado y certificado: orden contenedores y después checks",
			in: Input{FromContainers: true, HostOK: true,
				Containers: []Container{{Name: "a", State: "running", Health: "unhealthy"}},
				Checks:     []Check{{Kind: "http", Results: 1, CertExpiry: now.Add(time.Hour)}}},
			want: Status{Degraded, []string{"a unhealthy", "certificado vence el 2026-11-10"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.in, now, p)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Evaluate:\n got %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestEvaluateUmbralesPropios(t *testing.T) {
	p := Params{FailThreshold: 1, RestartWindow: time.Minute, CertWarn: time.Hour}
	got := Evaluate(Input{Checks: []Check{{Kind: "tcp", Results: 1, Failures: 1, Detail: "timeout"}}}, now, p)
	if got.State != Down {
		t.Errorf("con FailThreshold 1, una falla tiene que tirar: %#v", got)
	}
	got = Evaluate(Input{FromContainers: true, HostOK: true, Containers: []Container{
		{Name: "a", State: "running", RestartedAt: now.Add(-2 * time.Minute)},
	}}, now, p)
	if got.State != Up {
		t.Errorf("reinicio fuera de la ventana de 1 minuto: %#v", got)
	}
}

func TestReason(t *testing.T) {
	if r := (Status{State: Up}).Reason(); r != "" {
		t.Errorf("Reason sin motivos = %q", r)
	}
	if r := (Status{Down, []string{"a", "b"}}).Reason(); r != "a" {
		t.Errorf("Reason = %q, quería a", r)
	}
}

func TestChange(t *testing.T) {
	down := func(r ...string) Status { return Status{Down, r} }
	deg := func(r ...string) Status { return Status{Degraded, r} }
	unk := func(r ...string) Status { return Status{Unknown, r} }
	tests := []struct {
		name       string
		prev, next Status
		want       Event
		ok         bool
	}{
		{"igual arriba", up(), up(), Event{}, false},
		{"igual caído con el mismo motivo", down("a detenido (exit 1)"), down("a detenido (exit 1)"), Event{}, false},
		{"mismo motivo principal, otros distintos", down("x", "y"), down("x", "z"), Event{}, false},
		{"caído con otro motivo", down("check http falla: timeout"), down("check http falla: código 502 (esperado 2xx)"),
			Event{Down, Down, "check http falla: código 502 (esperado 2xx)"}, true},
		{"arriba a caído", up(), down("a detenido (exit 137)"), Event{Up, Down, "a detenido (exit 137)"}, true},
		{"arriba a degradado", up(), deg("a unhealthy"), Event{Up, Degraded, "a unhealthy"}, true},
		{"caído por check a arriba", down("a detenido (exit 0)", "check tcp falla: timeout"), up(),
			Event{Down, Up, "volvió a responder"}, true},
		{"caído por push a arriba", down("push: no llegó en 1h"), up(), Event{Down, Up, "volvió a responder"}, true},
		{"degradado por contenedor a arriba", deg("a reiniciado (exit 137)"), up(), Event{Degraded, Up, "arriba"}, true},
		{"a sin datos no es evento", up(), unk("sin datos del agent"), Event{}, false},
		{"de nunca guardado a arriba", Status{}, up(), Event{"", Up, "arriba"}, true},
		{"de sin datos a caído", unk("sin datos todavía"), down("push: no llegó"), Event{Unknown, Down, "push: no llegó"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Change(tt.prev, tt.next)
			if ok != tt.ok || got != tt.want {
				t.Errorf("Change = %#v, %v; quería %#v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestEvaluateUnit(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tests := []struct {
		name   string
		unit   Unit
		hostOK bool
		checks []Check
		want   Status
	}{
		{"servicio activo", Unit{Kind: "service", Name: "a.service", Active: "active"}, true, nil, Status{State: Up}},
		{"servicio recargando", Unit{Kind: "service", Name: "a.service", Active: "reloading"}, true, nil, Status{State: Up}},
		{"servicio falló con exit", Unit{Kind: "service", Name: "a.service", Active: "failed", Result: "exit-code", ExitStatus: 3}, true, nil, Status{Down, []string{"a.service falló (exit 3)"}}},
		{"servicio falló por señal", Unit{Kind: "service", Name: "a.service", Active: "failed", Result: "signal"}, true, nil, Status{Down, []string{"a.service falló (signal)"}}},
		{"servicio falló sin motivo", Unit{Kind: "service", Name: "a.service", Active: "failed"}, true, nil, Status{Down, []string{"a.service falló"}}},
		{"servicio detenido", Unit{Kind: "service", Name: "a.service", Active: "inactive"}, true, nil, Status{Stopped, []string{"a.service detenido"}}},
		{"servicio arrancando", Unit{Kind: "service", Name: "a.service", Active: "activating"}, true, nil, Status{Unknown, []string{"a.service arrancando"}}},
		{"servicio deteniéndose", Unit{Kind: "service", Name: "a.service", Active: "deactivating"}, true, nil, Status{Unknown, []string{"a.service deteniéndose"}}},
		{"no existe", Unit{Kind: "service", Name: "b.service", Missing: true}, true, nil, Status{Down, []string{"b.service no existe en el host"}}},
		{"timer bien", Unit{Kind: "timer", Name: "r.timer", Active: "active", Result: "success"}, true, nil, Status{State: Up}},
		{"timer sin corridas", Unit{Kind: "timer", Name: "r.timer", Active: "active"}, true, nil, Status{State: Up}},
		{"timer falló", Unit{Kind: "timer", Name: "r.timer", Active: "active", Result: "exit-code", ExitStatus: 1}, true, nil, Status{Down, []string{"última corrida falló (exit 1)"}}},
		{"timer detenido", Unit{Kind: "timer", Name: "r.timer", Active: "inactive", Result: "success"}, true, nil, Status{Stopped, []string{"timer detenido"}}},
		{"timer detenido que falló", Unit{Kind: "timer", Name: "r.timer", Active: "inactive", Result: "exit-code", ExitStatus: 2}, true, nil, Status{Down, []string{"última corrida falló (exit 2)"}}},
		{"lxc prendida", Unit{Kind: "lxc", Name: "dns", Active: "running"}, true, nil, Status{State: Up}},
		{"vm apagada", Unit{Kind: "vm", Name: "win", Active: "stopped"}, true, nil, Status{Stopped, []string{"win apagada"}}},
		{"vm pausada", Unit{Kind: "vm", Name: "win", Active: "paused"}, true, nil, Status{Degraded, []string{"win: paused"}}},
		{"agent caído", Unit{Kind: "service", Name: "a.service", Active: "failed"}, false, nil, Status{Unknown, []string{"sin datos del agent"}}},
		{"agent caído con check", Unit{Kind: "service", Name: "a.service"}, false, []Check{{Kind: "http", Results: 1}}, Status{State: Up}},
		{"unidad y check caen", Unit{Kind: "service", Name: "a.service", Active: "inactive"}, true, []Check{{Kind: "http", Results: 3, Failures: 3, Detail: "timeout"}},
			Status{Down, []string{"check http falla: timeout"}}},
		{"detenido con check que degrada", Unit{Kind: "service", Name: "a.service", Active: "inactive"}, true, []Check{{Kind: "http", Results: 1, Failures: 1, Detail: "timeout"}},
			Status{Degraded, []string{"check http falla: timeout"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := tt.unit
			got := Evaluate(Input{Unit: &u, HostOK: tt.hostOK, Checks: tt.checks}, now, DefaultParams)
			if got.State != tt.want.State || !slices.Equal(got.Reasons, tt.want.Reasons) {
				t.Fatalf("= %+v, quería %+v", got, tt.want)
			}
		})
	}
}
