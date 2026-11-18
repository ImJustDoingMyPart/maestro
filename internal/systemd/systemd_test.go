package systemd

import (
	"testing"
	"time"
)

func TestMonotonicUsec(t *testing.T) {
	now := 100 * time.Hour // el host arrancó hace 100 horas
	// Vence a las 101 h desde el arranque: dentro de una hora.
	got := monotonicUsec(uint64((101 * time.Hour).Microseconds()), now)
	if d := time.Until(got); d < 59*time.Minute || d > 61*time.Minute {
		t.Errorf("vence en %v, quería una hora", d)
	}
	for _, v := range []any{uint64(0), ^uint64(0), "x", nil} {
		if !monotonicUsec(v, now).IsZero() {
			t.Errorf("%v tendría que dar cero", v)
		}
	}
	if monotonicNow() <= 0 {
		t.Error("CLOCK_MONOTONIC no se pudo leer")
	}
}
