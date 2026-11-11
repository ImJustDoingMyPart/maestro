package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// External es un servicio externo creado desde la interfaz.
type External struct {
	ID          string
	URL         string // link de la tarjeta; "" si no tiene
	Description string
	Kind        string // "http" o "tcp"
	Target      string // URL o host:puerto
	Expect      int    // HTTP: código esperado; 0 es cualquier 2xx
	NoRedirects bool
	Interval    time.Duration // 0: el de quien corre el check
	Timeout     time.Duration
}

// Externals da los servicios externos, por ID.
func (s *Store) Externals(ctx context.Context) ([]External, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, url, description, check_kind, target, expect, no_redirects, interval_ms, timeout_ms
		FROM externals ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []External
	for rows.Next() {
		var (
			e        External
			int_, to int64
		)
		if err := rows.Scan(&e.ID, &e.URL, &e.Description, &e.Kind, &e.Target, &e.Expect, &e.NoRedirects, &int_, &to); err != nil {
			return nil, err
		}
		e.Interval, e.Timeout = time.Duration(int_)*time.Millisecond, time.Duration(to)*time.Millisecond
		out = append(out, e)
	}
	return out, rows.Err()
}

// External busca uno por ID.
func (s *Store) External(ctx context.Context, id string) (External, error) {
	all, err := s.Externals(ctx)
	if err != nil {
		return External{}, err
	}
	for _, e := range all {
		if e.ID == id {
			return e, nil
		}
	}
	return External{}, ErrNotFound
}

// CreateExternal guarda uno nuevo con su nombre y su grupo (group "" lo
// deja en Otros). ErrExists si el ID ya está.
func (s *Store) CreateExternal(ctx context.Context, e External, name, group string, at time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO externals (id, url, description, check_kind, target, expect, no_redirects, interval_ms, timeout_ms, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ID, e.URL, e.Description, e.Kind, e.Target, e.Expect, e.NoRedirects,
			e.Interval.Milliseconds(), e.Timeout.Milliseconds(), ms(at))
		if isUniqueErr(err) {
			return ErrExists
		}
		if err != nil {
			return err
		}
		return setExternalPrefs(ctx, tx, e.ID, name, group, at)
	})
}

// UpdateExternal cambia uno existente. ErrNotFound si no está.
func (s *Store) UpdateExternal(ctx context.Context, e External, name, group string, at time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE externals SET url = ?, description = ?, check_kind = ?, target = ?, expect = ?,
				no_redirects = ?, interval_ms = ?, timeout_ms = ?
			WHERE id = ?`,
			e.URL, e.Description, e.Kind, e.Target, e.Expect, e.NoRedirects,
			e.Interval.Milliseconds(), e.Timeout.Milliseconds(), e.ID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrNotFound
		}
		return setExternalPrefs(ctx, tx, e.ID, name, group, at)
	})
}

func setExternalPrefs(ctx context.Context, tx *sql.Tx, id, name, group string, at time.Time) error {
	key := "service:" + id
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO card_names (key, name, at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET name = excluded.name, at = excluded.at`, key, name, ms(at)); err != nil {
		return err
	}
	if group == "" {
		_, err := tx.ExecContext(ctx, `DELETE FROM card_groups WHERE key = ?`, key)
		return err
	}
	return setCardGroups(ctx, tx, []string{key}, group, at)
}

// DeleteExternal borra uno y todo lo de su tarjeta (nombre, grupo, orden,
// oculto). Borrar uno que no existe no es error.
func (s *Store) DeleteExternal(ctx context.Context, id string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM externals WHERE id = ?`,
			`DELETE FROM card_names WHERE key = 'service:' || ?`,
			`DELETE FROM card_groups WHERE key = 'service:' || ?`,
			`DELETE FROM card_order WHERE key = 'service:' || ?`,
			`DELETE FROM hidden WHERE key = 'service:' || ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// isUniqueErr reconoce la violación de una clave primaria o UNIQUE.
func isUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	var coder interface{ Code() int }
	if errors.As(err, &coder) {
		// SQLITE_CONSTRAINT_PRIMARYKEY (1555) y SQLITE_CONSTRAINT_UNIQUE (2067).
		return coder.Code() == 1555 || coder.Code() == 2067
	}
	return false
}
