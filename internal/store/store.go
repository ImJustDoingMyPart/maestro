package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registra el driver "sqlite" en database/sql
)

//go:embed migrations/*.sql
var migrations embed.FS

// ErrNotFound sale de las consultas que no encuentran la fila pedida.
var ErrNotFound = errors.New("no encontrado")

// Store es la base SQLite del hub. Es seguro usarlo desde varias goroutines:
// database/sql maneja un pool de conexiones.
type Store struct {
	db *sql.DB
}

// Open abre (o crea) la base en path y corre las migraciones pendientes.
//
// La base tiene hashes de contraseñas y de sesiones: se crea con permisos
// 600 y, si ya existía con permisos más abiertos, se le cierran. SQLite crea
// el -wal y el -shm con los permisos de la base.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := restrictPerms(path); err != nil {
		return nil, err
	}
	// WAL deja leer mientras otro escribe; busy_timeout espera un lock en
	// vez de fallar enseguida; foreign_keys no viene activado por defecto.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("migraciones: %w", err)
	}
	return s, nil
}

// restrictPerms crea path vacío con permisos 600 si no existe, y le saca
// los permisos de grupo y otros si los tiene.
func restrictPerms(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		return f.Close()
	}
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			if err := os.Chmod(p, info.Mode().Perm()&0o700); err != nil {
				return fmt.Errorf("cerrar permisos de %s: %w", p, err)
			}
		}
	}
	return nil
}

// Close cierra la base.
func (s *Store) Close() error { return s.db.Close() }

// migrate corre, en orden, las migraciones con número mayor que
// PRAGMA user_version. Cada una va en su transacción junto con el cambio de
// user_version, así que una migración a medias no queda aplicada.
func (s *Store) migrate(ctx context.Context) error {
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	slices.Sort(files)

	var current int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return err
	}

	for i, f := range files {
		// "001_usuarios_y_sesiones.sql" → 1. El número tiene que coincidir
		// con la posición: un hueco o un repetido es un error de programación.
		num, err := strconv.Atoi(strings.SplitN(path.Base(f), "_", 2)[0])
		if err != nil || num != i+1 {
			return fmt.Errorf("%s: las migraciones tienen que numerarse 001, 002, … sin huecos", f)
		}
		if num <= current {
			continue
		}
		body, err := migrations.ReadFile(f)
		if err != nil {
			return err
		}
		if err := s.inTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return err
			}
			// PRAGMA no acepta parámetros: el número sale de un Atoi, no de
			// datos externos.
			_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", num))
			return err
		}); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
	}
	return nil
}

func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback() // el error que importa es el de fn
		return err
	}
	return tx.Commit()
}

// User es un usuario del panel.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
}

// SetPassword crea el usuario o le cambia la contraseña. Al cambiarla se
// borran sus sesiones: quien tuviera una abierta tiene que volver a entrar.
func (s *Store) SetPassword(ctx context.Context, username, hash string, now time.Time) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var id int64
		err := tx.QueryRowContext(ctx, `
			INSERT INTO users (username, password_hash, created_at, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (username) DO UPDATE
			SET password_hash = excluded.password_hash, updated_at = excluded.updated_at
			RETURNING id`,
			username, hash, now.Unix(), now.Unix()).Scan(&id)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)
		return err
	})
}

// UserByName busca un usuario por nombre.
func (s *Store) UserByName(ctx context.Context, username string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// HasUsers dice si hay al menos un usuario: sin usuarios no se puede entrar,
// y serve lo avisa al arrancar.
func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n > 0, err
}

// CreateSession guarda una sesión nueva.
func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, userID int64, now, expires time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		tokenHash, userID, now.Unix(), expires.Unix())
	return err
}

// SessionUser devuelve el usuario de una sesión vigente.
func (s *Store) SessionUser(ctx context.Context, tokenHash []byte, now time.Time) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.password_hash
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`,
		tokenHash, now.Unix(),
	).Scan(&u.ID, &u.Username, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// DeleteSession borra una sesión (logout). Borrar una que no existe no es error.
func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteExpiredSessions borra las sesiones vencidas y dice cuántas eran.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
