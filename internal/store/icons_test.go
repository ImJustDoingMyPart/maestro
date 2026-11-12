package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestIconsYOrdenDeHosts(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	now := time.Now()
	if err := s.SetIcon(ctx, "found:pve/lxc/102", "adguard-home", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIcon(ctx, "found:pve/lxc/102", "sh-adguard-home", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIcon(ctx, "stack:vm/x", "immich", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIcon(ctx, "stack:vm/x", "", now); err != nil {
		t.Fatal(err)
	}
	got, err := s.Icons(ctx)
	if err != nil || len(got) != 1 || got["found:pve/lxc/102"] != "sh-adguard-home" {
		t.Fatalf("Icons = %v, %v", got, err)
	}

	if h, err := s.HWHosts(ctx); err != nil || h != nil {
		t.Fatalf("sin orden: %q, %v", h, err)
	}
	// Un host fijado de antes va primero.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO ui_prefs (key, value) VALUES ('hw_pinned', 'pve')`); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.HWHosts(ctx); !slices.Equal(h, []string{"pve"}) {
		t.Errorf("con fijado de antes = %q", h)
	}
	s.SetHWHosts(ctx, []string{"vm", "pve"})
	s.SetHWHosts(ctx, []string{"pve", "vm", "adguard"})
	if h, _ := s.HWHosts(ctx); !slices.Equal(h, []string{"pve", "vm", "adguard"}) {
		t.Errorf("orden = %q", h)
	}
}
