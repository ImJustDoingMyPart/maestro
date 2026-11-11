package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "maestro.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestMigracionesYPragmas(t *testing.T) {
	s, path := openTest(t)
	ctx := context.Background()

	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 6 {
		t.Errorf("user_version = %d, quería 6", version)
	}
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, quería wal", mode)
	}
	var fk int
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Error("foreign_keys desactivado")
	}

	// Reabrir no vuelve a correr las migraciones (fallaría el CREATE TABLE).
	s.Close()
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reabrir: %v", err)
	}
	s2.Close()
}

func TestUsuariosYSesiones(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)

	if has, _ := s.HasUsers(ctx); has {
		t.Fatal("base nueva con usuarios")
	}
	if _, err := s.UserByName(ctx, "admin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UserByName de inexistente = %v, quería ErrNotFound", err)
	}

	if err := s.SetPassword(ctx, "admin", "hash1", now); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByName(ctx, "admin")
	if err != nil || u.PasswordHash != "hash1" {
		t.Fatalf("UserByName = %+v, %v", u, err)
	}
	if has, _ := s.HasUsers(ctx); !has {
		t.Error("HasUsers = false después de crear uno")
	}

	tok := []byte("hash-del-token-32-bytes-xxxxxxxx")
	if err := s.CreateSession(ctx, tok, u.ID, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SessionUser(ctx, tok, now.Add(time.Minute)); err != nil || got.ID != u.ID {
		t.Errorf("SessionUser vigente = %+v, %v", got, err)
	}
	if _, err := s.SessionUser(ctx, tok, now.Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Errorf("SessionUser vencida = %v, quería ErrNotFound", err)
	}

	// Cambiar la contraseña conserva el usuario y cierra sus sesiones.
	if err := s.SetPassword(ctx, "admin", "hash2", now); err != nil {
		t.Fatal(err)
	}
	u2, _ := s.UserByName(ctx, "admin")
	if u2.ID != u.ID || u2.PasswordHash != "hash2" {
		t.Errorf("después de SetPassword: %+v", u2)
	}
	if _, err := s.SessionUser(ctx, tok, now); !errors.Is(err, ErrNotFound) {
		t.Error("la sesión sobrevivió al cambio de contraseña")
	}

	// Logout y limpieza de vencidas.
	if err := s.CreateSession(ctx, tok, u.ID, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, tok, now); !errors.Is(err, ErrNotFound) {
		t.Error("la sesión sobrevivió al logout")
	}
	_ = s.CreateSession(ctx, []byte("vieja"), u.ID, now, now.Add(time.Minute))
	_ = s.CreateSession(ctx, []byte("nueva"), u.ID, now, now.Add(time.Hour))
	if n, err := s.DeleteExpiredSessions(ctx, now.Add(10*time.Minute)); err != nil || n != 1 {
		t.Errorf("DeleteExpiredSessions = %d, %v; quería 1", n, err)
	}
}

func TestCreateFirstUser(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	now := time.Now()

	// Dos a la vez: gana uno solo.
	errs := make(chan error, 2)
	for _, name := range []string{"a", "b"} {
		go func() { errs <- s.CreateFirstUser(ctx, name, "hash", now) }()
	}
	var ok, exist int
	for range 2 {
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.Is(err, ErrUsersExist):
			exist++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || exist != 1 {
		t.Errorf("creados %d, rechazados %d; quería 1 y 1", ok, exist)
	}
	if err := s.CreateFirstUser(ctx, "c", "hash", now); !errors.Is(err, ErrUsersExist) {
		t.Errorf("con un usuario ya creado: %v", err)
	}
}
