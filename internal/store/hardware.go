package store

import (
	"context"
	"database/sql"
)

// HWItem es la preferencia de un elemento de la pantalla Hardware.
type HWItem struct {
	Position int // -1: sin orden propio
	Hidden   bool
}

// HWLayout da el orden y los ocultos de Hardware de un host, por elemento.
func (s *Store) HWLayout(ctx context.Context, host string) (map[string]HWItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item, position, hidden FROM hw_layout WHERE host = ?`, host)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]HWItem{}
	for rows.Next() {
		var (
			item   string
			pos    sql.NullInt64
			hidden int
		)
		if err := rows.Scan(&item, &pos, &hidden); err != nil {
			return nil, err
		}
		it := HWItem{Position: -1, Hidden: hidden != 0}
		if pos.Valid {
			it.Position = int(pos.Int64)
		}
		out[item] = it
	}
	return out, rows.Err()
}

// SetHWOrder guarda el orden de un grupo de elementos de un host (los
// mosaicos, o los gráficos): items, del primero al último. No toca si
// están ocultos.
func (s *Store) SetHWOrder(ctx context.Context, host string, items []string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for i, item := range items {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO hw_layout (host, item, position) VALUES (?, ?, ?)
				ON CONFLICT (host, item) DO UPDATE SET position = excluded.position`, host, item, i); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetHWHidden oculta o muestra un elemento de Hardware de un host.
func (s *Store) SetHWHidden(ctx context.Context, host, item string, hidden bool) error {
	h := 0
	if hidden {
		h = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO hw_layout (host, item, hidden) VALUES (?, ?, ?)
		ON CONFLICT (host, item) DO UPDATE SET hidden = excluded.hidden`, host, item, h)
	return err
}
