package store

import (
	"context"
	"database/sql"
	"time"
)

// CheckResult es un resultado guardado de un check.
type CheckResult struct {
	Service    string
	Kind       string
	At         time.Time
	OK         bool
	Latency    time.Duration
	Detail     string
	CertExpiry time.Time // cero si no aplica
}

// StateEvent es un cambio de estado guardado.
type StateEvent struct {
	ID       int64
	Service  string
	At       time.Time
	From, To string
	Reason   string
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(v int64) time.Time { return time.UnixMilli(v) }

// AddCheckResult guarda un resultado.
func (s *Store) AddCheckResult(ctx context.Context, r CheckResult) error {
	var cert sql.NullInt64
	if !r.CertExpiry.IsZero() {
		cert = sql.NullInt64{Int64: ms(r.CertExpiry), Valid: true}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO check_results (service, kind, at, ok, latency_ms, detail, cert_expiry)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.Service, r.Kind, ms(r.At), r.OK, r.Latency.Milliseconds(), r.Detail, cert)
	return err
}

func scanResults(rows *sql.Rows) ([]CheckResult, error) {
	defer rows.Close()
	var out []CheckResult
	for rows.Next() {
		var (
			r       CheckResult
			at, lat int64
			cert    sql.NullInt64
		)
		if err := rows.Scan(&r.Service, &r.Kind, &at, &r.OK, &lat, &r.Detail, &cert); err != nil {
			return nil, err
		}
		r.At, r.Latency = fromMS(at), time.Duration(lat)*time.Millisecond
		if cert.Valid {
			r.CertExpiry = fromMS(cert.Int64)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CheckResults da los resultados de un servicio desde since, del más viejo
// al más nuevo.
func (s *Store) CheckResults(ctx context.Context, service string, since time.Time) ([]CheckResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT service, kind, at, ok, latency_ms, detail, cert_expiry
		FROM check_results WHERE service = ? AND at >= ? ORDER BY at, rowid`,
		service, ms(since))
	if err != nil {
		return nil, err
	}
	return scanResults(rows)
}

// LatestCheckResults da los últimos n resultados de cada check de todos los
// servicios, del más nuevo al más viejo dentro de cada uno. Es lo que el
// hub lee al arrancar para saber cuántas fallas seguidas lleva cada check.
func (s *Store) LatestCheckResults(ctx context.Context, n int) ([]CheckResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT service, kind, at, ok, latency_ms, detail, cert_expiry FROM (
			SELECT *, row_number() OVER (PARTITION BY service, kind ORDER BY at DESC, rowid DESC) AS n
			FROM check_results
		) WHERE n <= ? ORDER BY service, kind, at DESC`, n)
	if err != nil {
		return nil, err
	}
	return scanResults(rows)
}

// LastOK da la hora del último resultado bueno de un check; cero si no hay.
func (s *Store) LastOK(ctx context.Context, service, kind string) (time.Time, error) {
	var at sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT max(at) FROM check_results WHERE service = ? AND kind = ? AND ok = 1`,
		service, kind).Scan(&at)
	if err != nil || !at.Valid {
		return time.Time{}, err
	}
	return fromMS(at.Int64), nil
}

// AddStateEvent guarda un cambio de estado y devuelve su ID.
func (s *Store) AddStateEvent(ctx context.Context, e StateEvent) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO state_events (service, at, from_state, to_state, reason) VALUES (?, ?, ?, ?, ?)`,
		e.Service, ms(e.At), e.From, e.To, e.Reason)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func scanEvents(rows *sql.Rows) ([]StateEvent, error) {
	defer rows.Close()
	var out []StateEvent
	for rows.Next() {
		var (
			e  StateEvent
			at int64
		)
		if err := rows.Scan(&e.ID, &e.Service, &at, &e.From, &e.To, &e.Reason); err != nil {
			return nil, err
		}
		e.At = fromMS(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastStateEvents da el último evento de cada servicio: el estado que el
// hub tenía guardado antes de reiniciarse.
func (s *Store) LastStateEvents(ctx context.Context) ([]StateEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, service, at, from_state, to_state, reason FROM state_events
		WHERE id IN (SELECT max(id) FROM state_events GROUP BY service)
		ORDER BY service`)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// StateEvents da los eventos de un servicio desde since, del más viejo al
// más nuevo, como mucho los últimos limit.
func (s *Store) StateEvents(ctx context.Context, service string, since time.Time, limit int) ([]StateEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, service, at, from_state, to_state, reason FROM (
			SELECT * FROM state_events WHERE service = ? AND at >= ?
			ORDER BY at DESC, id DESC LIMIT ?
		) ORDER BY at, id`,
		service, ms(since), limit)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// Compact pasa a check_hourly los resultados anteriores a la hora en que
// cae keepFrom y los borra de check_results. Corta en una hora entera para
// que ninguna hora quede repartida entre las dos tablas a medias. Borra
// además los resúmenes anteriores a dropHourlyBefore. Dice cuántos
// resultados compactó.
func (s *Store) Compact(ctx context.Context, keepFrom, dropHourlyBefore time.Time) (int64, error) {
	cut := ms(keepFrom.Truncate(time.Hour))
	var n int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		// at - at % 3600000 es el comienzo de la hora (at es positivo).
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO check_hourly (service, kind, hour, total, ok, latency_sum_ms)
			SELECT service, kind, at - at % 3600000, count(*), sum(ok), sum(latency_ms)
			FROM check_results WHERE at < ?
			GROUP BY service, kind, at - at % 3600000
			ON CONFLICT (service, kind, hour) DO UPDATE SET
				total = total + excluded.total,
				ok = ok + excluded.ok,
				latency_sum_ms = latency_sum_ms + excluded.latency_sum_ms`, cut); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM check_results WHERE at < ?`, cut)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		_, err = tx.ExecContext(ctx, `DELETE FROM check_hourly WHERE hour < ?`, ms(dropHourlyBefore))
		return err
	})
	return n, err
}

// HourlySummary es la disponibilidad de un check en una hora.
type HourlySummary struct {
	Service, Kind string
	Hour          time.Time
	Total, OK     int
	AvgLatency    time.Duration
}

// HourlySummaries da los resúmenes de un servicio desde since, en orden.
func (s *Store) HourlySummaries(ctx context.Context, service string, since time.Time) ([]HourlySummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT service, kind, hour, total, ok, latency_sum_ms FROM check_hourly
		WHERE service = ? AND hour >= ? ORDER BY hour, kind`, service, ms(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HourlySummary
	for rows.Next() {
		var (
			h         HourlySummary
			hour, sum int64
		)
		if err := rows.Scan(&h.Service, &h.Kind, &hour, &h.Total, &h.OK, &sum); err != nil {
			return nil, err
		}
		h.Hour = fromMS(hour)
		if h.Total > 0 {
			h.AvgLatency = time.Duration(sum/int64(h.Total)) * time.Millisecond
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
