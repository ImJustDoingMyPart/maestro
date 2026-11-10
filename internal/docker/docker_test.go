package docker

import (
	"reflect"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

func TestFromSummary(t *testing.T) {
	labels := map[string]string{"com.docker.compose.service": "jellyfin"}
	tests := []struct {
		name     string
		in       container.Summary
		state    *container.State
		restarts int
		want     agent.Container
	}{
		{
			name: "con healthcheck",
			in: container.Summary{
				ID: "abc", Names: []string{"/jellyfin"}, Image: "jellyfin/jellyfin:latest",
				State: container.StateRunning, Labels: labels,
				Health: &container.HealthSummary{Status: container.Healthy},
			},
			state:    &container.State{ExitCode: 137, StartedAt: "2026-11-10T15:04:05.123456789Z"},
			restarts: 3,
			want: agent.Container{
				ID: "abc", Name: "jellyfin", Image: "jellyfin/jellyfin:latest",
				State: "running", Health: "healthy", Restarts: 3, Labels: labels,
				ExitCode: 137, StartedAt: time.Date(2026, 11, 10, 15, 4, 5, 123456789, time.UTC),
			},
		},
		{
			name: "sin healthcheck declarado, nunca arrancó",
			in: container.Summary{
				ID: "def", Names: []string{"/caddy"}, State: container.StateExited,
				Health: &container.HealthSummary{Status: container.NoHealthcheck},
			},
			state: &container.State{StartedAt: "0001-01-01T00:00:00Z"},
			want:  agent.Container{ID: "def", Name: "caddy", State: "exited"},
		},
		{
			name: "sin datos de health ni nombre",
			in:   container.Summary{ID: "ghi", State: container.StateCreated},
			want: agent.Container{ID: "ghi", State: "created"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fromSummary(tt.in, tt.state, tt.restarts); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("fromSummary:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}
