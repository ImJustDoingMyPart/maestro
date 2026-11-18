// Package systemd lee las unidades de systemd del host por D-Bus: los
// servicios y los timers cargados, con su estado y, de los timers, la
// última y la próxima corrida y cómo terminó el servicio que disparan.
//
// Habla con el bus del sistema (/run/dbus/system_bus_socket), que deja
// leer sin ser root. En un contenedor, alcanza con montar ese socket.
package systemd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	sd "github.com/coreos/go-systemd/v22/dbus"
	"golang.org/x/sys/unix"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

// Reader lee las unidades. Abre la conexión al bus la primera vez y la
// vuelve a abrir si se corta (dbus reiniciado).
type Reader struct {
	mu   sync.Mutex
	conn *sd.Conn
}

// New crea un lector; la conexión se abre en el primer Units.
func New() *Reader { return &Reader{} }

// Close cierra la conexión, si hay.
func (r *Reader) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn != nil {
		r.conn.Close()
		r.conn = nil
	}
}

func (r *Reader) connect(ctx context.Context) (*sd.Conn, error) {
	if r.conn != nil && r.conn.Connected() {
		return r.conn, nil
	}
	if r.conn != nil {
		r.conn.Close()
	}
	c, err := sd.NewSystemConnectionContext(ctx)
	if err != nil {
		r.conn = nil
		return nil, fmt.Errorf("bus del sistema: %w", err)
	}
	r.conn = c
	return c, nil
}

// Units da los servicios y timers cargados, ordenados por nombre. Cumple
// collect.UnitSource.
func (r *Reader) Units(ctx context.Context) ([]agent.Unit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, err := r.connect(ctx)
	if err != nil {
		return nil, err
	}
	list, err := c.ListUnitsContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("listar unidades: %w", err)
	}
	var out []agent.Unit
	for _, u := range list {
		if u.LoadState != "loaded" || !(strings.HasSuffix(u.Name, ".service") || strings.HasSuffix(u.Name, ".timer")) {
			continue
		}
		out = append(out, agent.Unit{Name: u.Name, Description: u.Description, Active: u.ActiveState, Sub: u.SubState})
	}

	for i := range out {
		u := &out[i]
		switch {
		case strings.HasSuffix(u.Name, ".timer"):
			p, err := c.GetUnitTypePropertiesContext(ctx, u.Name, "Timer")
			if err != nil {
				continue // la unidad se fue entre el listado y esto
			}
			u.Triggers, _ = p["Unit"].(string)
			u.LastRun = usec(p["LastTriggerUSec"])
			u.NextRun = usec(p["NextElapseUSecRealtime"])
			if next := monotonicUsec(p["NextElapseUSecMonotonic"], monotonicNow()); u.NextRun.IsZero() || (!next.IsZero() && next.Before(u.NextRun)) {
				// Un timer con OnBootSec u OnUnitActiveSec (sin
				// OnCalendar) vence en tiempo monotónico: desde el
				// arranque del host. Si tiene los dos, el que llegue
				// antes.
				u.NextRun = next
			}
			if u.Triggers != "" {
				u.Result, u.ExitStatus = serviceResult(ctx, c, u.Triggers)
			}
		case u.Active == "failed":
			// De los demás servicios solo importa el motivo cuando
			// fallaron: así no son dos llamadas por unidad en cada vuelta.
			u.Result, u.ExitStatus = serviceResult(ctx, c, u.Name)
		}
	}
	slices.SortFunc(out, func(a, b agent.Unit) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// serviceResult lee Result y ExecMainStatus de un servicio.
func serviceResult(ctx context.Context, c *sd.Conn, name string) (string, int) {
	p, err := c.GetUnitTypePropertiesContext(ctx, name, "Service")
	if err != nil {
		return "", 0
	}
	res, _ := p["Result"].(string)
	st, _ := p["ExecMainStatus"].(int32)
	return res, int(st)
}

// monotonicUsec pasa un vencimiento en el reloj monotónico (µs desde el
// arranque) a la hora de pared, con now el reloj monotónico actual. Cero
// si no hay. El reloj monotónico es el del kernel: en un contenedor es el
// mismo que en el host.
func monotonicUsec(v any, now time.Duration) time.Time {
	n, ok := v.(uint64)
	if !ok || n == 0 || n == ^uint64(0) || now <= 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(n)*time.Microsecond - now).Truncate(time.Second)
}

// monotonicNow es CLOCK_MONOTONIC; 0 si no se pudo leer.
func monotonicNow() time.Duration {
	var ts unix.Timespec
	if unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts) != nil {
		return 0
	}
	return time.Duration(ts.Nano())
}

// usec pasa una propiedad en microsegundos Unix a time.Time; cero si no
// hay (systemd usa 0 para "nunca").
func usec(v any) time.Time {
	n, ok := v.(uint64)
	if !ok || n == 0 || n == ^uint64(0) {
		return time.Time{}
	}
	return time.UnixMicro(int64(n))
}
