package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestExternos(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)

	router := External{ID: "router", Kind: "tcp", Target: "192.168.0.1:80", Interval: time.Minute}
	if err := s.CreateExternal(ctx, router, "Router", "Red", now); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateExternal(ctx, router, "Otro", "", now); !errors.Is(err, ErrExists) {
		t.Errorf("ID repetido: %v", err)
	}
	web := External{ID: "web", URL: "https://web.example.org", Description: "Sitio", Kind: "http",
		Target: "https://web.example.org/salud", Expect: 204, NoRedirects: true, Timeout: 5 * time.Second}
	if err := s.CreateExternal(ctx, web, "Web", "", now); err != nil {
		t.Fatal(err)
	}
	got, err := s.Externals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []External{router, web}) {
		t.Errorf("externos = %+v", got)
	}
	names, _ := s.Names(ctx)
	groups, _ := s.CardGroups(ctx)
	if names["service:router"] != "Router" || groups["service:router"] != "Red" || names["service:web"] != "Web" {
		t.Errorf("nombres %v, grupos %v", names, groups)
	}
	if _, ok := groups["service:web"]; ok {
		t.Error("sin grupo no tiene que guardar fila")
	}

	router.Target = "192.168.0.1:443"
	if err := s.UpdateExternal(ctx, router, "Router casa", "", now); err != nil {
		t.Fatal(err)
	}
	if e, err := s.External(ctx, "router"); err != nil || e.Target != "192.168.0.1:443" {
		t.Errorf("después de editar: %+v %v", e, err)
	}
	groups, _ = s.CardGroups(ctx)
	if _, ok := groups["service:router"]; ok {
		t.Error("grupo vacío al editar no lo sacó")
	}
	if err := s.UpdateExternal(ctx, External{ID: "nada", Kind: "tcp"}, "x", "", now); !errors.Is(err, ErrNotFound) {
		t.Errorf("editar uno que no existe: %v", err)
	}

	if err := s.Hide(ctx, "service:router", now); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteExternal(ctx, "router"); err != nil {
		t.Fatal(err)
	}
	names, _ = s.Names(ctx)
	hidden, _ := s.Hidden(ctx)
	if _, err := s.External(ctx, "router"); !errors.Is(err, ErrNotFound) {
		t.Errorf("borrado sigue: %v", err)
	}
	if _, ok := names["service:router"]; ok {
		t.Error("borrar no limpió el nombre")
	}
	if _, ok := hidden["service:router"]; ok {
		t.Error("borrar no limpió el oculto")
	}
}
