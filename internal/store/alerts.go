package store

import (
	"context"
	"time"
)

// SetAlert prende (true) o apaga (false) los avisos de un servicio. nil
// borra lo de la interfaz: vuelve lo que digan los labels o el valor por
// defecto.
func (s *Store) SetAlert(ctx context.Context, key string, alert *bool, at time.Time) error {
	if alert == nil {
		_, err := s.db.ExecContext(ctx, `DELETE FROM alerts WHERE key = ?`, key)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO alerts (key, alert, at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET alert = excluded.alert, at = excluded.at`,
		key, *alert, ms(at))
	return err
}

// Alerts da lo puesto con SetAlert, por clave.
func (s *Store) Alerts(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, alert FROM alerts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var (
			key   string
			alert bool
		)
		if err := rows.Scan(&key, &alert); err != nil {
			return nil, err
		}
		out[key] = alert
	}
	return out, rows.Err()
}
