package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SetIcon pone el ícono de una tarjeta. Vacío lo borra: vuelve el de los
// labels o el que se adivina.
func (s *Store) SetIcon(ctx context.Context, key, icon string, at time.Time) error {
	if icon == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM card_icons WHERE key = ?`, key)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO card_icons (key, icon, at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET icon = excluded.icon, at = excluded.at`,
		key, icon, ms(at))
	return err
}

// Icons da los íconos elegidos desde la interfaz, por clave.
func (s *Store) Icons(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, icon FROM card_icons`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, icon string
		if err := rows.Scan(&key, &icon); err != nil {
			return nil, err
		}
		out[key] = icon
	}
	return out, rows.Err()
}

// HWPinned es el host que Hardware muestra primero; "" si no hay.
func (s *Store) HWPinned(ctx context.Context) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM ui_prefs WHERE key = 'hw_pinned'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetHWPinned fija el host que Hardware muestra primero. "" lo saca.
func (s *Store) SetHWPinned(ctx context.Context, host string) error {
	if host == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM ui_prefs WHERE key = 'hw_pinned'`)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ui_prefs (key, value) VALUES ('hw_pinned', ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, host)
	return err
}
