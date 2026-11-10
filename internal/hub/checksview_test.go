package hub

import (
	"testing"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

func TestCoverage(t *testing.T) {
	h := func(n float64) time.Time { return t0.Add(time.Duration(n * float64(time.Hour))) }
	ok := func(n float64) store.CheckResult { return store.CheckResult{At: h(n), OK: true} }
	fail := func(n float64) store.CheckResult { return store.CheckResult{At: h(n)} }
	const d = 2 * time.Hour
	tests := []struct {
		name           string
		results        []store.CheckResult
		from, to       float64
		covered, known float64 // horas
	}{
		{"sin resultados", nil, 0, 10, 0, 0},
		{"una llamada cubre su plazo", []store.CheckResult{ok(0)}, 0, 10, 2, 10},
		{"llamadas encimadas no se cuentan dos veces", []store.CheckResult{ok(0), ok(1), ok(2.5)}, 0, 10, 4.5, 10},
		{"las fallas no cubren", []store.CheckResult{ok(0), fail(2.2), fail(4.2), ok(5)}, 0, 10, 4, 10},
		{"una llamada de antes de la ventana cubre el principio", []store.CheckResult{ok(-1)}, 0, 10, 1, 10},
		{"los datos empiezan con el primer resultado", []store.CheckResult{ok(4)}, 0, 10, 2, 6},
		{"si el primero es falla, empiezan un plazo antes", []store.CheckResult{fail(5)}, 0, 10, 0, 7},
		{"al día todo el tramo", []store.CheckResult{ok(0), ok(1.5), ok(3)}, 1, 4, 3, 3},
		{"tramo antes de los datos", []store.CheckResult{ok(5)}, 0, 2, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, k := coverage(tt.results, d, h(tt.from), h(tt.to))
			if c != time.Duration(tt.covered*float64(time.Hour)) || k != time.Duration(tt.known*float64(time.Hour)) {
				t.Errorf("coverage = %v cubierto de %v, quería %vh de %vh", c, k, tt.covered, tt.known)
			}
		})
	}
}

func TestPushUptimeYBarras(t *testing.T) {
	now := t0.Add(24 * time.Hour)
	// Restic: un push hace 30 h con plazo de 96 h + 10 %: todo el día al día.
	res := []store.CheckResult{{At: now.Add(-30 * time.Hour), OK: true}}
	d := 96*time.Hour + 96*time.Hour/10
	if got := pushUptime(res, d, now.Add(-24*time.Hour), now); got != "100 %" {
		t.Errorf("pushUptime = %q", got)
	}
	for _, b := range pushBars(res, d, now, 24*time.Hour, 48) {
		if b.Class != "up" {
			t.Fatalf("barra %+v, quería up", b)
		}
	}

	// Plazo de 1 h: llamó a las 0 y no más; a las 12 falló y siguió fallando.
	res = []store.CheckResult{{At: t0, OK: true}, {At: t0.Add(time.Hour)}, {At: t0.Add(2 * time.Hour)}}
	if got := pushUptime(res, time.Hour, t0, t0.Add(4*time.Hour)); got != "25,0 %" {
		t.Errorf("pushUptime = %q, quería 25,0 %%", got)
	}
	bs := pushBars(res, time.Hour, t0.Add(4*time.Hour), 4*time.Hour, 4)
	if bs[0].Class != "up" || bs[1].Class != "down" || bs[3].Class != "down" {
		t.Errorf("barras: %+v", bs)
	}
}

func TestPercent(t *testing.T) {
	for _, tt := range []struct {
		a, b float64
		want string
	}{{1, 1, "100 %"}, {999, 1000, "99,9 %"}, {9999, 10000, "99,9 %"}, {0, 5, "0,0 %"}, {1, 3, "33,3 %"}} {
		if got := percent(tt.a, tt.b); got != tt.want {
			t.Errorf("percent(%v, %v) = %q, quería %q", tt.a, tt.b, got, tt.want)
		}
	}
}
