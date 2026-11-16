package hub

import (
	"context"
	"time"

	"github.com/ImJustDoingMyPart/maestro/internal/status"
	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

// snapState es el estado que daría el monitor con solo los contenedores de
// la foto, sin checks ni reinicios vistos.
func snapState(svcID string, snap Snapshot) State { return State(snapStatus(svcID, snap).State) }

func snapStatus(id string, snap Snapshot) status.Status {
	svc, ok := findService(snap, id)
	if !ok {
		return status.Status{State: status.Unknown}
	}
	return status.Evaluate(buildInput(svc, snap.HostOK(svc.Host), unitFor(svc, snap.Systems), nil, nil), time.Now(), status.DefaultParams)
}

// fakeMonitor responde el estado a partir de la foto, y lo demás vacío o
// lo que el test le cargue.
type fakeMonitor struct {
	snap    Snapshot
	checks  map[string][]CheckView
	results []store.CheckResult
	events  []store.StateEvent
	live    chan store.StateEvent
	tokens  map[string]bool
	pushes  []fakePush
}

type fakePush struct {
	token, msg string
	down       bool
}

func (f *fakeMonitor) Status(id string) status.Status { return snapStatus(id, f.snap) }
func (f *fakeMonitor) Checks(id string) []CheckView   { return f.checks[id] }
func (f *fakeMonitor) Push(token string, down bool, msg string) bool {
	if f.tokens[token] {
		f.pushes = append(f.pushes, fakePush{token: token, msg: msg, down: down})
	}
	return f.tokens[token]
}
func (f *fakeMonitor) History(ctx context.Context, id string, since time.Time) ([]store.CheckResult, []store.StateEvent, error) {
	return f.results, f.events, nil
}
func (f *fakeMonitor) Subscribe(id string) (<-chan store.StateEvent, func()) {
	if f.live == nil {
		return nil, func() {}
	}
	return f.live, func() {}
}
