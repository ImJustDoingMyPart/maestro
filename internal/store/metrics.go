package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Resoluciones de las series, en segundos.
const (
	ResMinute = 60
	Res10Min  = 600
	ResHour   = 3600
)

// resolutions son las que se guardan, de la más fina a la más gruesa.
var resolutions = []int{ResMinute, Res10Min, ResHour}

// MetricPoint es un intervalo de una serie.
type MetricPoint struct {
	At       time.Time // comienzo del intervalo
	Avg, Max float64
}

// AddMetrics suma una muestra a todas las series de values, en las tres
// resoluciones. Crea las series que no existen. Va todo en una
// transacción: una vuelta del hub son unas cientos de series.
func (s *Store) AddMetrics(ctx context.Context, at time.Time, values map[string]float64) error {
	if len(values) == 0 {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		getID, err := tx.PrepareContext(ctx, `
			INSERT INTO metric_series (key) VALUES (?)
			ON CONFLICT (key) DO UPDATE SET key = excluded.key
			RETURNING id`)
		if err != nil {
			return err
		}
		defer getID.Close()
		add, err := tx.PrepareContext(ctx, `
			INSERT INTO metric_points (series, res, at, sum, n, max) VALUES (?, ?, ?, ?, 1, ?)
			ON CONFLICT (series, res, at) DO UPDATE SET
				sum = sum + excluded.sum, n = n + 1, max = max(max, excluded.max)`)
		if err != nil {
			return err
		}
		defer add.Close()
		for key, v := range values {
			var id int64
			if err := getID.QueryRowContext(ctx, key).Scan(&id); err != nil {
				return fmt.Errorf("serie %s: %w", key, err)
			}
			for _, res := range resolutions {
				bucket := at.Truncate(time.Duration(res) * time.Second)
				if _, err := add.ExecContext(ctx, id, res, ms(bucket), v, v); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// Metrics da los puntos de las series pedidas con resolución res desde
// since, en orden. Las series sin puntos no aparecen en el mapa.
func (s *Store) Metrics(ctx context.Context, keys []string, res int, since time.Time) (map[string][]MetricPoint, error) {
	out := map[string][]MetricPoint{}
	if len(keys) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(keys)+2)
	args = append(args, res, ms(since))
	for _, k := range keys {
		args = append(args, k)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.key, p.at, p.sum / p.n, p.max
		FROM metric_points p JOIN metric_series s ON s.id = p.series
		WHERE p.res = ? AND p.at >= ? AND s.key IN (?`+strings.Repeat(", ?", len(keys)-1)+`)
		ORDER BY s.key, p.at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			key string
			at  int64
			p   MetricPoint
		)
		if err := rows.Scan(&key, &at, &p.Avg, &p.Max); err != nil {
			return nil, err
		}
		p.At = fromMS(at)
		out[key] = append(out[key], p)
	}
	return out, rows.Err()
}

// MetricKeys da las claves de serie que empiezan con prefix, en orden.
func (s *Store) MetricKeys(ctx context.Context, prefix string) ([]string, error) {
	// Se compara por rango y no con LIKE: las claves pueden tener "_" y
	// "%", que en LIKE son comodines.
	rows, err := s.db.QueryContext(ctx,
		`SELECT key FROM metric_series WHERE key >= ? AND key < ? ORDER BY key`, prefix, prefix+"\U0010FFFF")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// MetricRetention es cuánto se guarda cada resolución. Las series de
// contenedores ("c/…") guardan las horas menos tiempo que las del host y
// los invitados: son muchas más, y para dimensionar el host alcanza con
// las suyas.
type MetricRetention struct {
	Minute, TenMin, Hour, ContainerHour time.Duration
}

// DefaultRetention: un día y algo de minutos (para ver las últimas 24 h
// enteras), dos semanas de 10 min, un año y un mes de horas (90 días para
// contenedores).
var DefaultRetention = MetricRetention{
	Minute: 26 * time.Hour, TenMin: 15 * 24 * time.Hour,
	Hour: 400 * 24 * time.Hour, ContainerHour: 90 * 24 * time.Hour,
}

// CompactMetrics borra los puntos vencidos y las series que se quedaron sin
// puntos (un contenedor que se dio de baja). Dice cuántos puntos borró.
func (s *Store) CompactMetrics(ctx context.Context, now time.Time, r MetricRetention) (int64, error) {
	var n int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		for _, d := range []struct {
			res int
			age time.Duration
		}{{ResMinute, r.Minute}, {Res10Min, r.TenMin}, {ResHour, r.Hour}} {
			res, err := tx.ExecContext(ctx, `DELETE FROM metric_points WHERE res = ? AND at < ?`, d.res, ms(now.Add(-d.age)))
			if err != nil {
				return err
			}
			k, _ := res.RowsAffected()
			n += k
		}
		res, err := tx.ExecContext(ctx, `
			DELETE FROM metric_points WHERE res = ? AND at < ?
			AND series IN (SELECT id FROM metric_series WHERE key >= 'c/' AND key < 'c0')`,
			ResHour, ms(now.Add(-r.ContainerHour)))
		if err != nil {
			return err
		}
		k, _ := res.RowsAffected()
		n += k
		_, err = tx.ExecContext(ctx, `
			DELETE FROM metric_series WHERE NOT EXISTS (SELECT 1 FROM metric_points WHERE series = metric_series.id)`)
		return err
	})
	return n, err
}
