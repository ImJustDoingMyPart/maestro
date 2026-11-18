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

// OutboxItem es un aviso que espera salir.
type OutboxItem struct {
	ID      int64
	Created time.Time
	Chat    int // posición en la lista de chats
	Service string
	Text    string
}

// AddOutbox guarda un aviso por mandar.
func (s *Store) AddOutbox(ctx context.Context, it OutboxItem) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO alert_outbox (created, chat, service, text) VALUES (?, ?, ?, ?)`,
		ms(it.Created), it.Chat, it.Service, it.Text)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Outbox da los avisos por mandar, del más viejo al más nuevo.
func (s *Store) Outbox(ctx context.Context) ([]OutboxItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, created, chat, service, text FROM alert_outbox ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxItem
	for rows.Next() {
		var (
			it      OutboxItem
			created int64
		)
		if err := rows.Scan(&it.ID, &created, &it.Chat, &it.Service, &it.Text); err != nil {
			return nil, err
		}
		it.Created = time.UnixMilli(created)
		out = append(out, it)
	}
	return out, rows.Err()
}

// DeleteOutbox borra un aviso que ya salió (o que no va a salir).
func (s *Store) DeleteOutbox(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM alert_outbox WHERE id = ?`, id)
	return err
}
