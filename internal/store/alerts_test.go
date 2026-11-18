package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestAvisos(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	yes, no := true, false
	for _, step := range []struct {
		key string
		v   *bool
	}{{"service:a/x", &yes}, {"found:vm/unit/b.service", &no}, {"service:a/z", &yes}, {"service:a/z", nil}, {"service:a/x", &no}} {
		if err := s.SetAlert(ctx, step.key, step.v, now); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Alerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]bool{"service:a/x": false, "found:vm/unit/b.service": false}; !reflect.DeepEqual(got, want) {
		t.Errorf("avisos = %v", got)
	}
}

func TestOutbox(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	a, err := s.AddOutbox(ctx, OutboxItem{Created: now, Chat: 0, Service: "media/jellyfin", Text: "uno"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddOutbox(ctx, OutboxItem{Created: now.Add(time.Second), Chat: 1, Service: "x", Text: "dos"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteOutbox(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := s.Outbox(ctx)
	if err != nil || len(got) != 1 || got[0].Text != "dos" || got[0].Chat != 1 || !got[0].Created.Equal(now.Add(time.Second)) {
		t.Fatalf("Outbox = %+v, %v", got, err)
	}
}
