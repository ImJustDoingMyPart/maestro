package store

import (
	"context"
	"testing"
)

func TestHWLayout(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	if err := s.SetHWOrder(ctx, "vm", []string{"tile:ram", "tile:cpu"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHWHidden(ctx, "vm", "chart:temp", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHWHidden(ctx, "vm", "tile:cpu", true); err != nil {
		t.Fatal(err)
	}
	// Reordenar no muestra lo oculto; mostrarlo no pierde el orden.
	if err := s.SetHWOrder(ctx, "vm", []string{"tile:cpu", "tile:ram"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.HWLayout(ctx, "vm")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]HWItem{
		"tile:cpu":   {Position: 0, Hidden: true},
		"tile:ram":   {Position: 1},
		"chart:temp": {Position: -1, Hidden: true},
	}
	if len(got) != len(want) {
		t.Fatalf("HWLayout = %v", got)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %+v; quería %+v", k, got[k], w)
		}
	}
	if other, _ := s.HWLayout(ctx, "pve"); len(other) != 0 {
		t.Errorf("otro host tiene %v", other)
	}
}
