package docker

import (
	"reflect"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
)

func TestFromSummary(t *testing.T) {
	labels := map[string]string{"com.docker.compose.service": "jellyfin"}
	tests := []struct {
		name     string
		in       container.Summary
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
			restarts: 3,
			want: agent.Container{
				ID: "abc", Name: "jellyfin", Image: "jellyfin/jellyfin:latest",
				State: "running", Health: "healthy", Restarts: 3, Labels: labels,
			},
		},
		{
			name: "sin healthcheck declarado",
			in: container.Summary{
				ID: "def", Names: []string{"/caddy"}, State: container.StateExited,
				Health: &container.HealthSummary{Status: container.NoHealthcheck},
			},
			want: agent.Container{ID: "def", Name: "caddy", State: "exited"},
		},
		{
			name: "sin datos de health ni nombre",
			in:   container.Summary{ID: "ghi", State: container.StateCreated},
			want: agent.Container{ID: "ghi", State: "created"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fromSummary(tt.in, tt.restarts); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("fromSummary:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}
