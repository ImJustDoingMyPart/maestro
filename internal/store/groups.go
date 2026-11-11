package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"
)

// ErrExists sale de las operaciones que crearían algo que ya existe.
var ErrExists = errors.New("ya existe")

// Groups da los grupos de la interfaz en orden.
func (s *Store) Groups(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM groups ORDER BY position, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// AddGroup agrega un grupo al final. ErrExists si ya está.
func (s *Store) AddGroup(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO groups (name, position)
		SELECT ?, coalesce(max(position), -1) + 1 FROM groups WHERE true
		ON CONFLICT (name) DO NOTHING`, name)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrExists
	}
	return nil
}

// CardGroups da el grupo puesto desde la interfaz de cada tarjeta, por
// clave. "" es "Otros".
func (s *Store) CardGroups(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, grp FROM card_groups`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, grp string
		if err := rows.Scan(&key, &grp); err != nil {
			return nil, err
		}
		out[key] = grp
	}
	return out, rows.Err()
}

// SetCardGroup manda una tarjeta a un grupo ("" es "Otros"). Un grupo que
// la interfaz no conocía se agrega al final.
func (s *Store) SetCardGroup(ctx context.Context, key, group string, at time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		return setCardGroups(ctx, tx, []string{key}, group, at)
	})
}

// ResetCardGroup borra el grupo de la interfaz de una tarjeta: vuelve el de
// los labels.
func (s *Store) ResetCardGroup(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM card_groups WHERE key = ?`, key)
	return err
}

func setCardGroups(ctx context.Context, tx *sql.Tx, keys []string, group string, at time.Time) error {
	if group != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO groups (name, position)
			SELECT ?, coalesce(max(position), -1) + 1 FROM groups WHERE true
			ON CONFLICT (name) DO NOTHING`, group); err != nil {
			return err
		}
	}
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO card_groups (key, grp, at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET grp = excluded.grp, at = excluded.at`,
			key, group, ms(at)); err != nil {
			return err
		}
	}
	return nil
}

// RenameGroup cambia el nombre de un grupo: las tarjetas que la interfaz
// ya mandaba a from pasan a to, y también las de keys (las que estaban en
// from por sus labels: el hub sabe cuáles son). Si to ya existe, los dos
// grupos quedan en uno, en el lugar de to.
func (s *Store) RenameGroup(ctx context.Context, from, to string, keys []string, at time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT count(*) > 0 FROM groups WHERE name = ?`, to).Scan(&exists); err != nil {
			return err
		}
		if exists {
			if _, err := tx.ExecContext(ctx, `DELETE FROM groups WHERE name = ?`, from); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `UPDATE groups SET name = ? WHERE name = ?`, to, from); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE card_groups SET grp = ?, at = ? WHERE grp = ?`, to, ms(at), from); err != nil {
			return err
		}
		return setCardGroups(ctx, tx, keys, to, at)
	})
}

// DeleteGroup borra un grupo: sus tarjetas (las que la interfaz mandaba
// ahí y las de keys) van a "Otros".
func (s *Store) DeleteGroup(ctx context.Context, name string, keys []string, at time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM groups WHERE name = ?`, name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE card_groups SET grp = '', at = ? WHERE grp = ?`, ms(at), name); err != nil {
			return err
		}
		return setCardGroups(ctx, tx, keys, "", at)
	})
}

// SetGroupOrder ordena los grupos como names. Los que la interfaz conocía
// y no están en names van después, en el orden que tenían.
func (s *Store) SetGroupOrder(ctx context.Context, names []string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT name FROM groups ORDER BY position, name`)
		if err != nil {
			return err
		}
		var old []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return err
			}
			old = append(old, n)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		order := slices.Clone(names)
		for _, n := range old {
			if !slices.Contains(order, n) {
				order = append(order, n)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM groups`); err != nil {
			return err
		}
		seen := map[string]bool{}
		pos := 0
		for _, n := range order {
			if seen[n] {
				continue
			}
			seen[n] = true
			if _, err := tx.ExecContext(ctx, `INSERT INTO groups (name, position) VALUES (?, ?)`, n, pos); err != nil {
				return err
			}
			pos++
		}
		return nil
	})
}

// CardOrder da la posición de cada tarjeta ordenada a mano, por clave. Las
// posiciones solo se comparan dentro de un grupo.
func (s *Store) CardOrder(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, position FROM card_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var (
			key string
			pos int
		)
		if err := rows.Scan(&key, &pos); err != nil {
			return nil, err
		}
		out[key] = pos
	}
	return out, rows.Err()
}

// SetCardOrder guarda el orden de las tarjetas de un grupo: keys, de la
// primera a la última.
func (s *Store) SetCardOrder(ctx context.Context, keys []string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for i, key := range keys {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO card_order (key, position) VALUES (?, ?)
				ON CONFLICT (key) DO UPDATE SET position = excluded.position`, key, i); err != nil {
				return err
			}
		}
		return nil
	})
}
