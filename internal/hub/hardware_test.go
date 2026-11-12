package hub

import (
	"testing"

	"github.com/ImJustDoingMyPart/maestro/internal/agent"
	"github.com/ImJustDoingMyPart/maestro/internal/catalog"
)

func TestTopStacksJuntaPorStack(t *testing.T) {
	svc := func(id, name, project, container string) catalog.Service {
		return catalog.Service{ID: id, Name: name, Project: project, Containers: []catalog.Container{{ID: container}}}
	}
	svcs := map[string]catalog.Service{
		"a1": svc("vm/arr/sonarr", "sonarr", "arr", "a1"),
		"a2": svc("vm/arr/radarr", "radarr", "arr", "a2"),
		"j1": svc("vm/jellyfin/jellyfin", "Jellyfin", "jellyfin", "j1"),
	}
	cs := []agent.ContainerStats{
		{ID: "a1", Name: "sonarr", CPU: 1, Mem: 300},
		{ID: "a2", Name: "radarr", CPU: 2, Mem: 500},
		{ID: "j1", Name: "jellyfin", CPU: 5, Mem: 700},
		{ID: "x", Name: "suelto", Mem: 100},
	}
	rows, more := topStacks("vm", cs, svcs)
	if more != 0 || len(rows) != 3 {
		t.Fatalf("filas = %d, más = %d", len(rows), more)
	}
	arr := rows[0]
	if arr.Name != "arr" || len(arr.Members) != 2 || arr.mem != 800 || arr.cpu != 3 || arr.Href != "/stacks/vm/arr" {
		t.Errorf("stack arr = %+v", arr)
	}
	if arr.Members[0].Name != "radarr" {
		t.Errorf("los contenedores no van por memoria: %+v", arr.Members)
	}
	// Un stack de un contenedor es una fila común que lleva a su servicio.
	if j := rows[1]; j.Name != "jellyfin" || j.Members != nil || j.Href != servicePath("vm/jellyfin/jellyfin") {
		t.Errorf("jellyfin = %+v", j)
	}
	if s := rows[2]; s.Name != "suelto" || s.Href != "" || s.Icon != "deployed_code" {
		t.Errorf("suelto = %+v", s)
	}

	many := make([]agent.ContainerStats, maxContainerRows+3)
	for i := range many {
		many[i] = agent.ContainerStats{ID: string(rune('a' + i)), Name: string(rune('a' + i))}
	}
	if rows, more := topStacks("vm", many, nil); len(rows) != maxContainerRows || more != 3 {
		t.Errorf("filas = %d, más = %d", len(rows), more)
	}
}
