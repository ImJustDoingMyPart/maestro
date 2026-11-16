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
