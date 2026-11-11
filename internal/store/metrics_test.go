package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestMetrics(t *testing.T) {
	ctx := context.Background()
	st, _ := openTest(t)
	t0 := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)

	// Cuatro muestras de 15 s: un minuto entero.
	for i, v := range []float64{10, 20, 30, 60} {
		at := t0.Add(time.Duration(i) * 15 * time.Second)
		if err := st.AddMetrics(ctx, at, map[string]float64{"h/vm/cpu": v, "c/vm/web/cpu": v / 10}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddMetrics(ctx, t0.Add(time.Minute), map[string]float64{"h/vm/cpu": 5}); err != nil {
		t.Fatal(err)
	}

	got, err := st.Metrics(ctx, []string{"h/vm/cpu", "nada"}, ResMinute, t0)
	if err != nil {
		t.Fatal(err)
	}
	want := []MetricPoint{{t0, 30, 60}, {t0.Add(time.Minute), 5, 5}}
	if len(got) != 1 || !slices.EqualFunc(got["h/vm/cpu"], want, func(a, b MetricPoint) bool {
		return a.At.Equal(b.At) && a.Avg == b.Avg && a.Max == b.Max
	}) {
		t.Fatalf("minutos = %+v", got)
	}

	hour, err := st.Metrics(ctx, []string{"h/vm/cpu"}, ResHour, t0.Add(-time.Hour))
	if err != nil || len(hour["h/vm/cpu"]) != 1 || hour["h/vm/cpu"][0].Avg != 25 || hour["h/vm/cpu"][0].Max != 60 {
		t.Fatalf("hora = %+v %v", hour, err)
	}

	keys, err := st.MetricKeys(ctx, "h/vm/")
	if err != nil || !slices.Equal(keys, []string{"h/vm/cpu"}) {
		t.Fatalf("claves = %v %v", keys, err)
	}

	// Un año después: se van los minutos y los 10 min; las horas del host
	// quedan, las del contenedor no.
	n, err := st.CompactMetrics(ctx, t0.Add(100*24*time.Hour), DefaultRetention)
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 { // minutos: 2 del host y 1 del contenedor; 10 min: 1 y 1; hora: la del contenedor
		t.Fatalf("borró %d", n)
	}
	keys, _ = st.MetricKeys(ctx, "")
	if !slices.Equal(keys, []string{"h/vm/cpu"}) {
		t.Fatalf("series que quedan = %v", keys)
	}
}
