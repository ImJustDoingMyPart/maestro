package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestGrupos(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)

	groups := func() []string {
		t.Helper()
		g, err := s.Groups(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	cards := func() map[string]string {
		t.Helper()
		c, err := s.CardGroups(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	if err := s.AddGroup(ctx, "Media"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGroup(ctx, "Red"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGroup(ctx, "Media"); !errors.Is(err, ErrExists) {
		t.Errorf("grupo repetido: %v", err)
	}
	// Mandar una tarjeta a un grupo nuevo lo agrega al final.
	if err := s.SetCardGroup(ctx, "service:a/x", "Archivos", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCardGroup(ctx, "service:a/y", "Media", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCardGroup(ctx, "service:b", "", now); err != nil {
		t.Fatal(err)
	}
	if g := groups(); !reflect.DeepEqual(g, []string{"Media", "Red", "Archivos"}) {
		t.Errorf("grupos = %v", g)
	}

	// Renombrar lleva las de la interfaz y las de keys.
	if err := s.RenameGroup(ctx, "Media", "Multimedia", []string{"stack:vm/jelly"}, now); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"service:a/x": "Archivos", "service:a/y": "Multimedia", "service:b": "", "stack:vm/jelly": "Multimedia"}
	if c := cards(); !reflect.DeepEqual(c, want) {
		t.Errorf("tarjetas = %v", c)
	}
	if g := groups(); !reflect.DeepEqual(g, []string{"Multimedia", "Red", "Archivos"}) {
		t.Errorf("grupos después de renombrar = %v", g)
	}
	// Renombrar a uno que existe los junta, en el lugar del existente.
	if err := s.RenameGroup(ctx, "Multimedia", "Archivos", nil, now); err != nil {
		t.Fatal(err)
	}
	if g := groups(); !reflect.DeepEqual(g, []string{"Red", "Archivos"}) {
		t.Errorf("grupos después de juntar = %v", g)
	}

	// Ordenar: los que faltan van al final.
	if err := s.AddGroup(ctx, "Sistema"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupOrder(ctx, []string{"Archivos", "Nuevo", "Archivos"}); err != nil {
		t.Fatal(err)
	}
	if g := groups(); !reflect.DeepEqual(g, []string{"Archivos", "Nuevo", "Red", "Sistema"}) {
		t.Errorf("grupos ordenados = %v", g)
	}

	// Borrar manda sus tarjetas a Otros.
	if err := s.DeleteGroup(ctx, "Archivos", []string{"service:z"}, now); err != nil {
		t.Fatal(err)
	}
	want = map[string]string{"service:a/x": "", "service:a/y": "", "service:b": "", "stack:vm/jelly": "", "service:z": ""}
	if c := cards(); !reflect.DeepEqual(c, want) {
		t.Errorf("tarjetas después de borrar = %v", c)
	}
	if err := s.ResetCardGroup(ctx, "service:z"); err != nil {
		t.Fatal(err)
	}
	if _, ok := cards()["service:z"]; ok {
		t.Error("ResetCardGroup no borró la fila")
	}
}

func TestOrdenDeTarjetas(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if err := s.SetCardOrder(ctx, []string{"service:b", "stack:vm/a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCardOrder(ctx, []string{"stack:vm/a", "service:b", "service:c"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.CardOrder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"stack:vm/a": 0, "service:b": 1, "service:c": 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("orden = %v", got)
	}
}
