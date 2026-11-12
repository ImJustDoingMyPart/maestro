package store

import (
	"context"
	"testing"
	"time"
)

func TestIconsYHostFijado(t *testing.T) {
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

	if h, err := s.HWPinned(ctx); err != nil || h != "" {
		t.Fatalf("sin fijar: %q, %v", h, err)
	}
	s.SetHWPinned(ctx, "pve")
	s.SetHWPinned(ctx, "vm")
	if h, _ := s.HWPinned(ctx); h != "vm" {
		t.Errorf("fijado = %q", h)
	}
	s.SetHWPinned(ctx, "")
	if h, _ := s.HWPinned(ctx); h != "" {
		t.Errorf("después de sacarlo = %q", h)
	}
}
