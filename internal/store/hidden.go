package store

import (
	"context"
	"time"
)

// Hide oculta una tarjeta. Ocultar una ya oculta no es error.
func (s *Store) Hide(ctx context.Context, key string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO hidden (key, at) VALUES (?, ?) ON CONFLICT (key) DO NOTHING`, key, ms(at))
	return err
}

// Unhide vuelve a mostrar una tarjeta. Mostrar una que no está oculta no
// es error.
func (s *Store) Unhide(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM hidden WHERE key = ?`, key)
	return err
}

// Hidden da las tarjetas ocultas con la hora en que se ocultaron.
func (s *Store) Hidden(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, at FROM hidden`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var (
			key string
			at  int64
		)
		if err := rows.Scan(&key, &at); err != nil {
			return nil, err
		}
		out[key] = fromMS(at)
	}
	return out, rows.Err()
}
