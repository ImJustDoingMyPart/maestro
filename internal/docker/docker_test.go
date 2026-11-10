package docker

import (
	"reflect"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"

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

func TestExitFromEvent(t *testing.T) {
	at := time.Date(2026, 11, 10, 21, 2, 29, 119299790, time.UTC)
	die := events.Message{
		Type: events.ContainerEventType, Action: events.ActionDie, TimeNano: at.UnixNano(),
		Actor: events.Actor{ID: "abc", Attributes: map[string]string{"exitCode": "137", "name": "jellyfin"}},
	}
	id, e, ok := exitFromEvent(die)
	if !ok || id != "abc" || e.code != 137 || !e.at.Equal(at) {
		t.Errorf("die: %q %+v %v", id, e, ok)
	}

	sinNano := die
	sinNano.TimeNano, sinNano.Time = 0, at.Unix()
	if _, e, _ := exitFromEvent(sinNano); !e.at.Equal(at.Truncate(time.Second)) {
		t.Errorf("sin TimeNano: %v", e.at)
	}

	for name, m := range map[string]events.Message{
		"otra acción":    {Type: events.ContainerEventType, Action: events.ActionStart, Actor: die.Actor},
		"otro tipo":      {Type: events.ImageEventType, Action: events.ActionDie, Actor: die.Actor},
		"sin exit code":  {Type: events.ContainerEventType, Action: events.ActionDie, Actor: events.Actor{ID: "abc"}},
		"sin contenedor": {Type: events.ContainerEventType, Action: events.ActionDie, Actor: events.Actor{Attributes: die.Actor.Attributes}},
	} {
		if _, _, ok := exitFromEvent(m); ok {
			t.Errorf("%s: no tendría que dar salida", name)
		}
	}
}
