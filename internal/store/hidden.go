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

// SetName pone el nombre de una tarjeta. Un nombre vacío lo borra: vuelve
// el de los labels.
func (s *Store) SetName(ctx context.Context, key, name string, at time.Time) error {
	if name == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM card_names WHERE key = ?`, key)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO card_names (key, name, at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET name = excluded.name, at = excluded.at`,
		key, name, ms(at))
	return err
}

// Names da los nombres puestos desde la interfaz, por clave.
func (s *Store) Names(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, name FROM card_names`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, name string
		if err := rows.Scan(&key, &name); err != nil {
			return nil, err
		}
		out[key] = name
	}
	return out, rows.Err()
}

// SetSeparate saca (true) o devuelve (false) un servicio de la tarjeta de su
// stack. nil borra lo de la interfaz: vuelve lo que digan los labels.
func (s *Store) SetSeparate(ctx context.Context, key string, separate *bool, at time.Time) error {
	if separate == nil {
		_, err := s.db.ExecContext(ctx, `DELETE FROM separated WHERE key = ?`, key)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO separated (key, separate, at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET separate = excluded.separate, at = excluded.at`,
		key, *separate, ms(at))
	return err
}

// Separated da lo puesto con SetSeparate, por clave.
func (s *Store) Separated(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, separate FROM separated`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var (
			key string
			sep bool
		)
		if err := rows.Scan(&key, &sep); err != nil {
			return nil, err
		}
		out[key] = sep
	}
	return out, rows.Err()
}

// Show pasa algo descubierto a la portada. Mostrar uno ya mostrado no es
// error.
func (s *Store) Show(ctx context.Context, key string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO shown (key, at) VALUES (?, ?) ON CONFLICT (key) DO NOTHING`, key, ms(at))
	return err
}

// Unshow lo devuelve a la bandeja.
func (s *Store) Unshow(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM shown WHERE key = ?`, key)
	return err
}

// Shown da lo descubierto que se muestra en la portada.
func (s *Store) Shown(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM shown`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out[key] = true
	}
	return out, rows.Err()
}
